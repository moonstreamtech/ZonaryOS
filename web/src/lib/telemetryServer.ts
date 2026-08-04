// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Server-side (Node runtime, i.e. this Next.js server process's own API
// route handlers) half of the frontend's usage telemetry - aggregate
// feature-usage COUNTS only, never content, matching
// internal/telemetry's absolute rule on the Go side (see that package's
// doc comment). Used from route handlers that already know an action
// succeeded (e.g. api/workflow/instances/route.ts after
// createInstance's `ok: true`) to increment a named event counter -
// never new instrumentation that reads business data, just a count of an
// outcome the route handler already observed.
//
// # Default-off guarantee
//
// Gated on ZONARYOS_TELEMETRY_ENABLED (server-only env var, no
// NEXT_PUBLIC_ prefix - deliberately distinct from the browser-side
// NEXT_PUBLIC_ZONARYOS_TELEMETRY_ENABLED flag in telemetry.ts, since this
// module never runs in the browser bundle at all and doesn't need a
// build-time-inlined value). recordFeatureEvent's first statement checks
// the flag and returns immediately when unset/not exactly "true" - no
// counter map is even touched, no flush timer is ever armed (start() is
// only called lazily, from inside the same guarded branch, the first
// time a recordFeatureEvent call actually has something to count).
import { apiBase } from "./telemetryShared";

const ENABLED = process.env.ZONARYOS_TELEMETRY_ENABLED === "true";

// FLUSH_INTERVAL_MS: PROVISIONAL DEFAULT, NOT A DECIDED value - same
// honesty as internal/telemetry.DefaultFlushInterval on the Go side,
// which this mirrors (5 minutes: infrequent enough to be obviously
// negligible cost, frequent enough that aggregate counts aren't stale
// for long).
const FLUSH_INTERVAL_MS = 5 * 60 * 1000;

let counts: Record<string, number> = {};
let timer: ReturnType<typeof setInterval> | null = null;

function ensureTimerStarted() {
  if (timer !== null) return;
  timer = setInterval(() => {
    void flush();
  }, FLUSH_INTERVAL_MS);
  // Node-only: don't let this timer keep the process alive on its own
  // (matters for graceful shutdown / short-lived serverless
  // invocations); harmless no-op in environments where unref doesn't
  // exist.
  timer.unref?.();
}

async function flush(): Promise<void> {
  const batch = counts;
  counts = {};
  if (Object.keys(batch).length === 0) return;

  try {
    await fetch(`${apiBase()}/telemetry/client-report`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ pageViewCounts: {}, featureEventCounts: batch }),
    });
  } catch {
    // Resilience guarantee (matches internal/telemetry's own boundary):
    // a failed flush is dropped, never thrown, never surfaced to
    // whatever route handler happened to trigger this timer tick -
    // there is no request in flight at all by the time this runs, but
    // even so, this must never crash the Next.js server process.
  }
}

/**
 * Increments a named feature-usage event counter by 1. No-op (does not
 * allocate, does not start a timer) unless ZONARYOS_TELEMETRY_ENABLED is
 * exactly "true" - see this module's doc comment for the full
 * default-off guarantee.
 */
export function recordFeatureEvent(event: string): void {
  if (!ENABLED) return;
  counts[event] = (counts[event] ?? 0) + 1;
  ensureTimerStarted();
}
