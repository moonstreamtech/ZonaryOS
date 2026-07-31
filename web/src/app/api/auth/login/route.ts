// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import {
  codeChallengeFromVerifier,
  generateCodeVerifier,
  generateState,
} from "@/lib/pkce";
import { requestOrigin } from "@/lib/requestOrigin";

const isProduction = process.env.NODE_ENV === "production";

// isSafeReturnTo restricts the `returnTo` query param (see below) to a
// same-origin, absolute-from-root path - never a protocol-relative or
// cross-origin URL. This is the same class of open-redirect guard as
// requestOrigin.ts's own reasoning: `returnTo` round-trips through a
// cookie set by this route and consumed by src/app/api/auth/callback,
// entirely server-controlled, but it still originates from a caller-
// supplied query string, so it's validated the same way any redirect
// target derived from user input should be.
function isSafeReturnTo(path: string): boolean {
  return path.startsWith("/") && !path.startsWith("//") && !path.includes("://");
}

export async function GET(request: NextRequest) {
  const issuer = process.env.ZONARYOS_KEYCLOAK_ISSUER_URL;
  const clientId = process.env.ZONARYOS_KEYCLOAK_CLIENT_ID;
  if (!issuer || !clientId) {
    return NextResponse.json(
      { error: "Keycloak is not configured" },
      { status: 500 },
    );
  }

  const verifier = generateCodeVerifier();
  const challenge = codeChallengeFromVerifier(verifier);
  const state = generateState();
  const redirectUri = `${requestOrigin(request)}/api/auth/callback`;

  // `returnTo`: where src/app/api/auth/callback should send the browser
  // after a successful login, instead of its default "/" - used by the
  // invite-accept flow (/invite/[token]) so a visitor who isn't signed in
  // yet lands back on the exact invite page they started from, not the
  // dashboard. Carried through the round-trip via a short-lived cookie
  // alongside the PKCE verifier/state, not folded into the `state` param
  // itself - `state` already has one job (CSRF-binding this specific
  // authorization request to this specific callback) and this codebase's
  // existing pattern for "value only this login attempt needs, matched
  // back up in the callback" is already a same-shaped short-lived cookie
  // (zonaryos_pkce_verifier), so this reuses that exact mechanism instead
  // of inventing a second way to smuggle state through the redirect.
  const { searchParams } = new URL(request.url);
  const returnTo = searchParams.get("returnTo");

  // `mode=register`: sends the visitor to Keycloak's own self-registration
  // form (.../protocol/openid-connect/registrations) instead of its login
  // form - see docs/DEVELOPMENT.md's "Invites" section for why this is
  // safe to offer with no email infrastructure (verifyEmail is off, see
  // deploy/keycloak/zonaryos-realm.json) and genuinely reachable through
  // this deployment's redirect/proxy setup. Same query params either way;
  // Keycloak's registrations endpoint accepts the identical
  // response_type/client_id/redirect_uri/scope/state/code_challenge set
  // the login endpoint does.
  const mode = searchParams.get("mode");
  const endpoint = mode === "register" ? "registrations" : "auth";

  const authUrl = new URL(
    `${issuer.replace(/\/$/, "")}/protocol/openid-connect/${endpoint}`,
  );
  authUrl.searchParams.set("response_type", "code");
  authUrl.searchParams.set("client_id", clientId);
  authUrl.searchParams.set("redirect_uri", redirectUri);
  authUrl.searchParams.set("scope", "openid profile email");
  authUrl.searchParams.set("state", state);
  authUrl.searchParams.set("code_challenge", challenge);
  authUrl.searchParams.set("code_challenge_method", "S256");

  const response = NextResponse.redirect(authUrl);
  const shortLivedCookie = {
    httpOnly: true,
    secure: isProduction,
    sameSite: "lax" as const,
    path: "/",
    maxAge: 600,
  };
  response.cookies.set("zonaryos_pkce_verifier", verifier, shortLivedCookie);
  response.cookies.set("zonaryos_oauth_state", state, shortLivedCookie);
  if (returnTo && isSafeReturnTo(returnTo)) {
    response.cookies.set("zonaryos_oauth_return_to", returnTo, shortLivedCookie);
  } else {
    // Clears any stale value from a previous login attempt so an old
    // returnTo can never leak into an unrelated, later login.
    response.cookies.delete("zonaryos_oauth_return_to");
  }
  return response;
}
