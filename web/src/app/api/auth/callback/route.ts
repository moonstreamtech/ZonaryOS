import { NextRequest, NextResponse } from "next/server";

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

  const redirectUri = new URL("/api/auth/callback", request.url).toString();

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
    expires_in?: number;
  };

  const response = NextResponse.redirect(new URL("/", request.url));
  // The access token is kept in an httpOnly cookie - never readable by
  // client-side JS - and forwarded as a Bearer token by server-side code
  // only (see src/app/api/me/route.ts and the homepage's server component).
  response.cookies.set("zonaryos_session", tokens.access_token, {
    httpOnly: true,
    secure: isProduction,
    sameSite: "lax",
    path: "/",
    maxAge: tokens.expires_in ?? 300,
  });
  response.cookies.delete("zonaryos_pkce_verifier");
  response.cookies.delete("zonaryos_oauth_state");
  return response;
}
