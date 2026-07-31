// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { requestOrigin } from "@/lib/requestOrigin";
import {
  DEFAULT_ACCESS_TOKEN_LIFESPAN_SECONDS,
  DEFAULT_REFRESH_TOKEN_LIFESPAN_SECONDS,
  REFRESH_COOKIE,
  SESSION_COOKIE,
  sessionCookieOptions,
} from "@/lib/session";

const isProduction = process.env.NODE_ENV === "production";

export async function GET(request: NextRequest) {
  const issuer = process.env.ZONARYOS_KEYCLOAK_ISSUER_URL;
  const clientId = process.env.ZONARYOS_KEYCLOAK_CLIENT_ID;
  if (!issuer || !clientId) {
    return NextResponse.json(
      { error: "Keycloak is not configured" },
      { status: 500 },
    );
  }

  const { searchParams } = new URL(request.url);
  const code = searchParams.get("code");
  const state = searchParams.get("state");
  const expectedState = request.cookies.get("zonaryos_oauth_state")?.value;
  const verifier = request.cookies.get("zonaryos_pkce_verifier")?.value;

  if (!code || !state || !expectedState || state !== expectedState || !verifier) {
    return NextResponse.json(
      { error: "Invalid or expired login attempt" },
      { status: 400 },
    );
  }

  const redirectUri = `${requestOrigin(request)}/api/auth/callback`;

  const tokenResponse = await fetch(
    `${issuer.replace(/\/$/, "")}/protocol/openid-connect/token`,
    {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        grant_type: "authorization_code",
        client_id: clientId,
        code,
        redirect_uri: redirectUri,
        code_verifier: verifier,
      }),
    },
  );

  if (!tokenResponse.ok) {
    return NextResponse.json(
      { error: "Token exchange with Keycloak failed" },
      { status: 502 },
    );
  }

  const tokens = (await tokenResponse.json()) as {
    access_token: string;
    refresh_token?: string;
    expires_in?: number;
    refresh_expires_in?: number;
  };

  // src/app/api/auth/login sets this alongside the PKCE verifier/state
  // when it was called with a `returnTo` query param (the invite-accept
  // flow) - re-validated here with the same isSafeReturnTo rule as
  // login's own check, since a cookie is still attacker-influenceable in
  // principle (this app's own login route is the only writer, but
  // defense in depth costs nothing here) before it's ever used as a
  // redirect target.
  const returnTo = request.cookies.get("zonaryos_oauth_return_to")?.value;
  const redirectTarget =
    returnTo && isSafeReturnTo(returnTo)
      ? `${requestOrigin(request)}${returnTo}`
      : `${requestOrigin(request)}/`;

  const response = NextResponse.redirect(redirectTarget);
  // The access token is kept in an httpOnly cookie - never readable by
  // client-side JS - and forwarded as a Bearer token by server-side code
  // only (see src/app/api/me/route.ts and the homepage's server component).
  response.cookies.set(
    SESSION_COOKIE,
    tokens.access_token,
    sessionCookieOptions(isProduction, tokens.expires_in ?? DEFAULT_ACCESS_TOKEN_LIFESPAN_SECONDS),
  );
  // The refresh token is stored the same way (httpOnly/secure/lax), and
  // is what src/proxy.ts uses to silently mint new access tokens without
  // the user ever seeing a re-login prompt - see docs/OPEN_POINTS.md item
  // 41 (session refresh bug) and docs/DEVELOPMENT.md's "Session refresh"
  // section. If Keycloak doesn't return a refresh token for some reason
  // (shouldn't happen with this client's default grant config), the
  // cookie is simply not set and every request behaves as it did before
  // this fix - the access token's own maxAge still applies, so this can
  // never leave a user *worse off* than before.
  if (tokens.refresh_token) {
    response.cookies.set(
      REFRESH_COOKIE,
      tokens.refresh_token,
      sessionCookieOptions(
        isProduction,
        tokens.refresh_expires_in ?? DEFAULT_REFRESH_TOKEN_LIFESPAN_SECONDS,
      ),
    );
  }
  response.cookies.delete("zonaryos_pkce_verifier");
  response.cookies.delete("zonaryos_oauth_state");
  response.cookies.delete("zonaryos_oauth_return_to");
  return response;
}

// Same rule as src/app/api/auth/login's own isSafeReturnTo - kept as a
// separate copy rather than a shared import specifically so each route
// validates independently at its own trust boundary (the value crosses a
// cookie in between), matching this codebase's general preference for an
// explicit re-check over trusting a value some other layer already
// validated once.
function isSafeReturnTo(path: string): boolean {
  return path.startsWith("/") && !path.startsWith("//") && !path.includes("://");
}
