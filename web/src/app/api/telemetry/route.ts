// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { apiBase } from "@/lib/telemetryShared";

// Forwards the browser's batched telemetry.ts payload (page-view +
// feature-event counts, never content - see that module's doc comment)
// to the Go backend's POST /telemetry/client-report, which merges it
// into internal/telemetry.Reporter's own counters. Unauthenticated on
// purpose - this is aggregate, non-content usage data, not a
// firm-scoped or user-scoped resource, so there is nothing here for a
// bearer-token check to protect.
//
// Gated on the same server-side ZONARYOS_TELEMETRY_ENABLED flag
// lib/telemetryServer.ts uses (not the browser's NEXT_PUBLIC_ variant -
// this handler runs server-side and can read the real one directly):
// when telemetry isn't enabled, this route does not forward anything at
// all and always answers 202 without touching the network - a defensive
// belt-and-braces guard against a stale cached bundle still holding
// NEXT_PUBLIC_ZONARYOS_TELEMETRY_ENABLED=true from a previous deploy.
//
// Always responds 202 regardless of outcome (including when the backend
// is unreachable or telemetry is disabled) - a telemetry beacon must
// never surface as a visible error to whatever page/unload event sent
// it, matching internal/telemetry's own "never a user-facing error"
// resilience rule on the Go side.
export async function POST(request: NextRequest) {
  if (process.env.ZONARYOS_TELEMETRY_ENABLED !== "true") {
    return NextResponse.json({ ok: true }, { status: 202 });
  }

  const body = await request.text().catch(() => "");
  if (body) {
    try {
      await fetch(`${apiBase()}/telemetry/client-report`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
      });
    } catch {
      // Dropped, not retried - see this route's doc comment.
    }
  }

  return NextResponse.json({ ok: true }, { status: 202 });
}
