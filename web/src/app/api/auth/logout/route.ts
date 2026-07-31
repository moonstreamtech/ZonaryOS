// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { requestOrigin } from "@/lib/requestOrigin";
import { REFRESH_COOKIE, SESSION_COOKIE } from "@/lib/session";

export async function GET(request: NextRequest) {
  const refreshToken = request.cookies.get(REFRESH_COOKIE)?.value;
  const issuer = process.env.ZONARYOS_KEYCLOAK_ISSUER_URL;
  const clientId = process.env.ZONARYOS_KEYCLOAK_CLIENT_ID;
  // Best-effort: ask Keycloak to revoke the refresh token server-side so
  // it can't be used again even if someone captured the cookie before it
  // was cleared below. Awaited (with the failure swallowed) rather than
  // truly fire-and-forget, since a Route Handler isn't guaranteed to keep
  // running background work after it returns a response - but a network
  // error talking to Keycloak must still never stop the user from being
  // logged out locally.
  if (refreshToken && issuer && clientId) {
    await fetch(`${issuer.replace(/\/$/, "")}/protocol/openid-connect/logout`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ client_id: clientId, refresh_token: refreshToken }),
      cache: "no-store",
    }).catch(() => {});
  }

  const response = NextResponse.redirect(`${requestOrigin(request)}/`);
  response.cookies.delete(SESSION_COOKIE);
  response.cookies.delete(REFRESH_COOKIE);
  return response;
}
