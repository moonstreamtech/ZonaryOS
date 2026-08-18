// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Offline support batch, Part 3: an IndexedDB-backed queue for write
// operations attempted while offline (localStorage is deliberately not
// used - smaller quota, and synchronous access blocks the main thread;
// IndexedDB is the standard fit for a queue that can grow and needs
// async access). This is a foundational library only - no existing form
// or mutation call site in the app is wired to enqueue through this yet
// (that would mean touching every write across the app, out of scope
// for this batch - see the batch brief's own scope boundary). What
// exists here is enqueue/replay/list, fully working, plus the
// `zonaryos:offline-queue-changed` CustomEvent dispatched on every
// mutation so a UI (Nav's sync status indicator) can stay in sync
// without polling, by listening for it (a poll fallback still works too
// since `listQueue` is cheap).

const DB_NAME = "zonaryos-offline-queue";
const DB_VERSION = 1;
const STORE_NAME = "operations";
export const QUEUE_CHANGED_EVENT = "zonaryos:offline-queue-changed";

export type QueuedOperationStatus = "pending" | "syncing" | "failed";

export interface QueuedOperation {
  id: string; // local UUID
  endpoint: string;
  method: string;
  body: unknown;
  queuedAt: string; // ISO 8601
  status: QueuedOperationStatus;
}

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: "id" });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

// Guarantees a strictly increasing `queuedAt` even when two operations
// are enqueued within the same millisecond (common in tests, and
// possible in real rapid-fire usage too) - `listQueue`/`replayQueue`
// sort by `queuedAt`, and the object store's own IndexedDB retrieval
// order (by primary key, a random UUID) is not a reliable proxy for
// insertion order, so this is what actually makes "oldest queuedAt
// first" mean "oldest enqueued first".
let lastQueuedAtMs = 0;

function nextQueuedAt(): string {
  let ms = Date.now();
  if (ms <= lastQueuedAtMs) ms = lastQueuedAtMs + 1;
  lastQueuedAtMs = ms;
  return new Date(ms).toISOString();
}

function generateId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  // Fallback for environments without crypto.randomUUID (older browsers) -
  // not cryptographically strong, but this is only a local queue-entry
  // key, not a security-sensitive identifier.
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function requestToPromise<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

function dispatchChanged() {
  if (typeof window !== "undefined" && typeof window.dispatchEvent === "function") {
    window.dispatchEvent(new CustomEvent(QUEUE_CHANGED_EVENT));
  }
}

/**
 * Enqueues a write operation to be replayed once connectivity returns.
 * Opens/creates the database and object store if needed, generates a
 * local UUID, and stores the operation with status "pending".
 */
export async function enqueueOperation(
  endpoint: string,
  method: string,
  body: unknown,
): Promise<QueuedOperation> {
  const operation: QueuedOperation = {
    id: generateId(),
    endpoint,
    method,
    body,
    queuedAt: nextQueuedAt(),
    status: "pending",
  };

  const db = await openDb();
  try {
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction(STORE_NAME, "readwrite");
      tx.objectStore(STORE_NAME).add(operation);
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error);
    });
  } finally {
    db.close();
  }

  dispatchChanged();
  return operation;
}

/**
 * Lists all queued operations, oldest `queuedAt` first - for a UI to show
 * pending/failed counts, or for `replayQueue` to determine replay order.
 */
export async function listQueue(): Promise<QueuedOperation[]> {
  const db = await openDb();
  try {
    const all = await requestToPromise(db.transaction(STORE_NAME, "readonly").objectStore(STORE_NAME).getAll());
    return [...all].sort((a, b) => a.queuedAt.localeCompare(b.queuedAt));
  } finally {
    db.close();
  }
}

async function putOperation(db: IDBDatabase, operation: QueuedOperation): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readwrite");
    tx.objectStore(STORE_NAME).put(operation);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

async function deleteOperation(db: IDBDatabase, id: string): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readwrite");
    tx.objectStore(STORE_NAME).delete(id);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

/**
 * Replays the queue IN ORDER, oldest `queuedAt` first.
 *
 * ORDERING GUARANTEE: operations are replayed strictly one at a time, in
 * queued order. On success an operation is removed from the queue and the
 * next one is attempted. On failure the operation is marked "failed" and
 * replay STOPS immediately - no reordering, no skipping past the failed
 * operation to try later ones. This matters because later queued
 * operations may depend on an earlier one having actually succeeded on
 * the server (e.g. a workflow-instance creation queued before a payment
 * recorded against that same instance) - replaying out of order, or
 * skipping a failure to "make progress" on later items, could apply a
 * payment against an instance that was never actually created. A failed
 * operation therefore blocks the rest of the queue until it is resolved
 * (retried successfully, e.g. by calling `replayQueue` again once
 * connectivity/the underlying issue is fixed).
 *
 * Call this manually, or wire it to the browser's `online` event, e.g.
 * `window.addEventListener("online", () => { void replayQueue(); })`.
 */
export async function replayQueue(): Promise<{ succeeded: string[]; stoppedAt: string | null }> {
  const succeeded: string[] = [];
  let stoppedAt: string | null = null;

  const queue = await listQueue();
  const db = await openDb();
  try {
    for (const operation of queue) {
      // A previously-failed operation still blocks the queue, but this
      // replay attempt tries it again in case the earlier failure was
      // transient (e.g. a network blip) - the queue doesn't need a
      // separate "retry" entry point for that common case.
      const syncing: QueuedOperation = { ...operation, status: "syncing" };
      await putOperation(db, syncing);
      dispatchChanged();

      try {
        const response = await fetch(operation.endpoint, {
          method: operation.method,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(operation.body),
        });
        if (!response.ok) {
          throw new Error(`Request failed with status ${response.status}`);
        }
        await deleteOperation(db, operation.id);
        succeeded.push(operation.id);
        dispatchChanged();
      } catch {
        const failed: QueuedOperation = { ...operation, status: "failed" };
        await putOperation(db, failed);
        dispatchChanged();
        stoppedAt = operation.id;
        break; // stop replaying - do not skip past a failed operation
      }
    }
  } finally {
    db.close();
  }

  return { succeeded, stoppedAt };
}

// Auto-wire replay to the browser's `online` event when this module runs
// in a browser (a no-op import in a server/test context that never calls
// this). Consumers that want manual control can still call
// `replayQueue()` directly at any time; this listener is additive, not a
// substitute.
if (typeof window !== "undefined") {
  window.addEventListener("online", () => {
    void replayQueue();
  });
}
