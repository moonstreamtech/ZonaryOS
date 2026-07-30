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

  const authUrl = new URL(
    `${issuer.replace(/\/$/, "")}/protocol/openid-connect/auth`,
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
  return response;
}
