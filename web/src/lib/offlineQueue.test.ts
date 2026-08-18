// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Offline support batch, Part 3: unit tests for the IndexedDB-backed
// offline queue. jsdom (this project's vitest environment - see
// vitest.config.ts) doesn't implement IndexedDB itself, so
// "fake-indexeddb/auto" (a devDependency added for this batch) patches
// `indexedDB` onto globalThis with a real, spec-shaped in-memory
// implementation - genuinely testing IndexedDB-shaped behavior (async
// requests/transactions/cursors) rather than a hand-rolled stub.
import "fake-indexeddb/auto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { enqueueOperation, listQueue, replayQueue } from "./offlineQueue";

// The queue's database name is a fixed constant inside offlineQueue.ts
// (not parameterized per-test), so each test deletes it first to start
// from a clean slate rather than accumulating state across tests.
async function resetDb() {
  await new Promise<void>((resolve, reject) => {
    const req = indexedDB.deleteDatabase("zonaryos-offline-queue");
    req.onsuccess = () => resolve();
    req.onerror = () => reject(req.error);
    req.onblocked = () => resolve();
  });
}

beforeEach(async () => {
  await resetDb();
  vi.restoreAllMocks();
});

afterEach(async () => {
  await resetDb();
});

describe("enqueueOperation", () => {
  it("stores the operation with status pending and a generated id", async () => {
    const op = await enqueueOperation("/api/v1/products", "POST", { name: "Widget" });

    expect(op.status).toBe("pending");
    expect(op.id).toBeTruthy();
    expect(op.endpoint).toBe("/api/v1/products");
    expect(op.method).toBe("POST");
    expect(op.body).toEqual({ name: "Widget" });

    const queue = await listQueue();
    expect(queue).toHaveLength(1);
    expect(queue[0]).toEqual(op);
  });

  it("lists multiple queued operations oldest queuedAt first", async () => {
    const first = await enqueueOperation("/api/v1/customers", "POST", { name: "A" });
    const second = await enqueueOperation("/api/v1/invoices", "POST", { name: "B" });

    const queue = await listQueue();
    expect(queue.map((op) => op.id)).toEqual([first.id, second.id]);
  });
});

describe("replayQueue ordering guarantee", () => {
  it("replays queued operations in order and removes each on success", async () => {
    const first = await enqueueOperation("/api/v1/workflow-instances", "POST", { a: 1 });
    const second = await enqueueOperation("/api/v1/payments", "POST", { b: 2 });

    const calledEndpoints: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        calledEndpoints.push(String(url));
        return new Response(null, { status: 200 });
      }),
    );

    const result = await replayQueue();

    expect(calledEndpoints).toEqual(["/api/v1/workflow-instances", "/api/v1/payments"]);
    expect(result.succeeded).toEqual([first.id, second.id]);
    expect(result.stoppedAt).toBeNull();
    expect(await listQueue()).toHaveLength(0);
  });

  it("stops replay at the first failure and does NOT skip ahead to later operations - a failed earlier operation must block later ones that may depend on it", async () => {
    const createInstance = await enqueueOperation("/api/v1/workflow-instances", "POST", { a: 1 });
    const recordPayment = await enqueueOperation("/api/v1/payments", "POST", { b: 2 });
    const thirdOp = await enqueueOperation("/api/v1/notifications", "POST", { c: 3 });

    const calledEndpoints: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        calledEndpoints.push(String(url));
        // The first operation (instance creation) fails; the second and
        // third would otherwise succeed.
        if (url === createInstance.endpoint) {
          return new Response(null, { status: 500 });
        }
        return new Response(null, { status: 200 });
      }),
    );

    const result = await replayQueue();

    // Only the first (failing) operation was even attempted - the second
    // and third were never called, proving replay stopped rather than
    // skipping past the failure.
    expect(calledEndpoints).toEqual([createInstance.endpoint]);
    expect(result.succeeded).toEqual([]);
    expect(result.stoppedAt).toBe(createInstance.id);

    const queue = await listQueue();
    expect(queue.map((op) => op.id)).toEqual([createInstance.id, recordPayment.id, thirdOp.id]);
    expect(queue[0]!.status).toBe("failed");
    // The later, never-attempted operations are untouched - still pending,
    // still in their original order.
    expect(queue[1]!.status).toBe("pending");
    expect(queue[2]!.status).toBe("pending");
  });

  it("retries a previously-failed operation on the next replay call, and resumes past it once it succeeds", async () => {
    const first = await enqueueOperation("/api/v1/workflow-instances", "POST", { a: 1 });
    const second = await enqueueOperation("/api/v1/payments", "POST", { b: 2 });

    let firstCallCount = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === first.endpoint) {
          firstCallCount += 1;
          if (firstCallCount === 1) return new Response(null, { status: 500 });
          return new Response(null, { status: 200 });
        }
        return new Response(null, { status: 200 });
      }),
    );

    const firstAttempt = await replayQueue();
    expect(firstAttempt.stoppedAt).toBe(first.id);
    expect((await listQueue()).map((op) => op.status)).toEqual(["failed", "pending"]);

    const secondAttempt = await replayQueue();
    expect(secondAttempt.succeeded).toEqual([first.id, second.id]);
    expect(secondAttempt.stoppedAt).toBeNull();
    expect(await listQueue()).toHaveLength(0);
  });
});
