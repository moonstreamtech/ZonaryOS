// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { NextRequest } from "next/server";

// Next.js's standalone production server (see docs/OPEN_POINTS.md item 34 -
// what `web/Dockerfile`'s final stage runs) always constructs `request.url`
// from its own bind hostname/port (HOSTNAME/PORT env, defaulting to
// "0.0.0.0") rather than the incoming request's Host header - a known
// standalone-mode limitation, not something a next.config.ts flag can
// override, since server.js unconditionally passes a hostname to Next's
// server constructor. That's harmless for most routes, but
// src/app/api/auth/login and .../callback both build an absolute
// redirect_uri from the request URL - and Keycloak's registered client
// only accepts `http://localhost:3000/*` (see deploy/keycloak/zonaryos-realm.json),
// so a "0.0.0.0"-based redirect_uri gets rejected outright. Building it from
// the actual Host header (which Next does forward correctly) instead of
// `request.url` sidesteps the bug without needing to patch Next itself.
export function requestOrigin(request: NextRequest): string {
  const proto = request.headers.get("x-forwarded-proto") ?? "http";
  const host = request.headers.get("host") ?? request.nextUrl.host;
  return `${proto}://${host}`;
}
