// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Browser-side usage telemetry batching client - page/screen view counts
// (which routes, how often) and feature-usage counts (named events the
// frontend already knows succeeded, e.g. "workflow_instance_created" -
// see components/Telemetry/TelemetryClient.tsx and the call sites that
// invoke recordFeatureEvent). Aggregate counts only, never content -
// mirrors internal/telemetry's absolute rule on the Go side exactly.
//
// # Default-off guarantee
//
// Gated on NEXT_PUBLIC_ZONARYOS_TELEMETRY_ENABLED (a build-time-inlined
// Next.js public env var - the NEXT_PUBLIC_ prefix is required for any
// value this module, which ships in the browser bundle, needs to read;
// see web/.env.example). init()'s first statement checks this flag and
// returns immediately when it isn't exactly "true" - no interval timer
// is armed, no `beforeunload`/`visibilitychange` listener is attached,
// no counters are ever incremented (recordPageView/recordFeatureEvent
// both also check the flag independently as their own first statement,
// so even a caller that skips init() entirely still can't cause a
// counter to be touched or a network request to be made). This matches
// internal/telemetry.Reporter's exact "check the one thing that matters
// before anything else can run" shape - see that package's doc comment.
const ENABLED = process.env.NEXT_PUBLIC_ZONARYOS_TELEMETRY_ENABLED === "true";

// FLUSH_INTERVAL_MS: PROVISIONAL DEFAULT, NOT A DECIDED value - mirrors
// internal/telemetry.DefaultFlushInterval (5 minutes) for the same
// reasons documented there: not a tuned number, just a defensible
// "batch, don't flush per-click" starting point (the task brief's own
// suggestion of "every few minutes").
const FLUSH_INTERVAL_MS = 5 * 60 * 1000;

let pageViewCounts: Record<string, number> = {};
let featureEventCounts: Record<string, number> = {};
let initialized = false;

function hasCounts(): boolean {
  return (
    Object.keys(pageViewCounts).length > 0 ||
    Object.keys(featureEventCounts).length > 0
  );
}

function flush() {
  if (!ENABLED || !hasCounts()) return;

  const body = JSON.stringify({
    pageViewCounts,
    featureEventCounts,
  });
  pageViewCounts = {};
  featureEventCounts = {};

  // navigator.sendBeacon is preferred (survives page unload, doesn't
  // block navigation) - fall back to a fire-and-forget fetch when
  // unavailable (older browsers, or a non-unload periodic tick where
  // either works equally well). Both paths deliberately ignore their
  // own outcome: a dropped telemetry batch must never surface as a
  // visible error or retry loop in the UI - matches internal/telemetry's
  // own resilience guarantee on the Go side.
  try {
    if (typeof navigator !== "undefined" && "sendBeacon" in navigator) {
      const blob = new Blob([body], { type: "application/json" });
      navigator.sendBeacon("/api/telemetry", blob);
      return;
    }
  } catch {
    // fall through to fetch
  }
  try {
    void fetch("/api/telemetry", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
      keepalive: true,
    });
  } catch {
    // Dropped, not retried inline - the next periodic tick will simply
    // report a fresh (larger) batch. See this module's doc comment.
  }
}

/**
 * Starts the periodic flush timer and unload/visibility-change
 * listeners. Safe to call multiple times (idempotent) - intended to be
 * called once from components/Telemetry/TelemetryClient.tsx, mounted
 * near the root layout. A true no-op when telemetry is disabled - see
 * this module's doc comment.
 */
export function init(): void {
  if (!ENABLED || initialized) return;
  initialized = true;

  const interval = setInterval(flush, FLUSH_INTERVAL_MS);
  // Flush on page unload/tab-hide, so a session shorter than
  // FLUSH_INTERVAL_MS still reports rather than losing its counts
  // entirely - this is exactly what sendBeacon is designed for.
  const onHide = () => {
    if (document.visibilityState === "hidden") flush();
  };
  window.addEventListener("pagehide", flush);
  document.addEventListener("visibilitychange", onHide);

  // Best-effort cleanup hook for tests/hot-reload - not required for
  // normal operation (the page is being torn down anyway), kept minimal.
  window.addEventListener("pagehide", () => clearInterval(interval), {
    once: true,
  });
}

/** Increments the view count for one route. No-op when disabled. */
export function recordPageView(route: string): void {
  if (!ENABLED) return;
  pageViewCounts[route] = (pageViewCounts[route] ?? 0) + 1;
}

/** Increments the count for one named feature-usage event. No-op when disabled. */
export function recordFeatureEvent(event: string): void {
  if (!ENABLED) return;
  featureEventCounts[event] = (featureEventCounts[event] ?? 0) + 1;
}
