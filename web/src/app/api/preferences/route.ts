// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchPreferences, patchPreferences, type UserPreferences } from "@/lib/preferences";

// Proxies the Go backend's GET/PATCH /api/me/preferences - unauthenticated-
// blocked, not firm-scoped (see internal/identity's own preferences.go
// doc comment: a user has preferences regardless of which firm they're
// viewing).
export async function GET(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const prefs = await fetchPreferences(token);
  if (!prefs) {
    return NextResponse.json({ error: "failed to load preferences" }, { status: 502 });
  }
  return NextResponse.json(prefs);
}

export async function PATCH(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const body = (await request.json().catch(() => ({}))) as UserPreferences;
  const prefs = await patchPreferences(token, body);
  if (!prefs) {
    return NextResponse.json({ error: "failed to update preferences" }, { status: 400 });
  }
  return NextResponse.json(prefs);
}
