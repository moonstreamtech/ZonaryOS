// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Tiny shared helper between telemetryServer.ts (Node runtime) and the
// /api/telemetry route it and the browser both ultimately funnel through
// - same ZONARYOS_API_BASE_URL convention every other lib/*.ts file
// already uses (see lib/firm.ts).
export function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}
