// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchMe } from "@/lib/me";
import { ACTIVE_FIRM_COOKIE } from "@/lib/activeFirm";

// Sets the active-firm cookie the firm switcher (components/Nav/FirmSwitcher.tsx)
// drives, and every server page's lib/activeFirm.ts resolveActiveFirm call
// reads. The requested firmId is validated against the caller's own
// memberships (via fetchMe) before being trusted - a stale or forged
// firmId is silently ignored (the cookie is only ever a UI preference:
// every backend call it later feeds into is independently membership-
// checked, so this validation is about not letting the switcher itself
// point the UI at a firm the caller doesn't belong to, not a security
// boundary in its own right).
export async function POST(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const body = (await request.json().catch(() => ({}))) as {
    firmId?: string;
  };
  if (!body.firmId) {
    return NextResponse.json({ error: "missing firmId" }, { status: 400 });
  }

  const me = await fetchMe(token);
  const isMember = me?.firms.some((f) => f.firmId === body.firmId) ?? false;
  if (!isMember) {
    return NextResponse.json({ error: "not a member of this firm" }, { status: 403 });
  }

  const response = NextResponse.json({ ok: true });
  response.cookies.set(ACTIVE_FIRM_COOKIE, body.firmId, {
    httpOnly: true,
    sameSite: "lax",
    path: "/",
  });
  return response;
}
