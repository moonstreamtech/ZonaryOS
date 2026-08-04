// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Shared cookie names/shapes/refresh logic for the session-refresh
// mechanism (see docs/DEVELOPMENT.md's "Session refresh" section). Kept
// here rather than duplicated between src/app/api/auth/callback/route.ts
// (which mints the pair on login) and src/proxy.ts (which silently
// refreshes the pair on every subsequent request) so both places agree on
// cookie names/options and on how a Keycloak token response gets read.

export const SESSION_COOKIE = "zonaryos_session";
export const REFRESH_COOKIE = "zonaryos_refresh_token";

// Keycloak's default realm settings (verified against
// deploy/keycloak/zonaryos-realm.json, which sets none of
// accessTokenLifespan/ssoSessionIdleTimeout/ssoSessionMaxLifespan/
// revokeRefreshToken explicitly, so the server's own built-in defaults for
// Keycloak 26.0 - the version pinned in docker-compose.yml - apply):
// accessTokenLifespan=300s (5 min), ssoSessionIdleTimeout=1800s (30 min),
// ssoSessionMaxLifespan=36000s (10h). A refresh token's own expiry
// (`refresh_expires_in` in the token response) tracks the *idle* timeout
// in this configuration (no offline access, no per-client override), so
// in practice: a session refreshes silently forever as long as some
// request happens at least once every ~30 minutes, and is force-ended
// after 10 hours regardless of activity. These constants are only a
// documented fallback for the (expected-never-to-happen) case where a
// Keycloak token response omits `expires_in`/`refresh_expires_in`
// entirely; the real values always come from the token response itself.
export const DEFAULT_ACCESS_TOKEN_LIFESPAN_SECONDS = 300;
export const DEFAULT_REFRESH_TOKEN_LIFESPAN_SECONDS = 1800;

// Refresh this many seconds before the access token's actual expiry, so a
// request that's mid-flight when the token would otherwise lapse never
// races a 401 from the Go backend. Deliberately generous relative to
// per-request latency on this stack.
export const REFRESH_SKEW_SECONDS = 30;

export type KeycloakTokenResponse = {
  access_token: string;
  refresh_token?: string;
  expires_in?: number;
  refresh_expires_in?: number;
};

export function sessionCookieOptions(isProduction: boolean, maxAge: number) {
  return {
    httpOnly: true,
    secure: isProduction,
    sameSite: "lax" as const,
    path: "/",
    maxAge: Math.max(maxAge, 1),
  };
}

/**
 * Reads the `exp` claim (seconds since epoch) out of a Keycloak access
 * token without verifying its signature - verification already happened
 * server-side (the Go backend validates the token on every request this
 * cookie is forwarded to); this is only used to decide *client-side*
 * whether a proactive refresh is worth attempting before the backend
 * would reject it. Returns null for anything that doesn't parse as a JWT
 * (never expected in practice, but a cookie value is still
 * attacker/tamper-influenceable in principle - fails closed to "treat as
 * expired, try to refresh" rather than throwing).
 */
export function decodeExpirySeconds(token: string): number | null {
  const parts = token.split(".");
  if (parts.length !== 3) return null;
  try {
    const base64 = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), "=");
    const json = atob(padded);
    const payload = JSON.parse(json) as { exp?: unknown };
    return typeof payload.exp === "number" ? payload.exp : null;
  } catch {
    return null;
  }
}

// True if `token` is missing, unparseable, or within REFRESH_SKEW_SECONDS
// of (or past) its `exp` claim.
export function isNearExpiry(token: string, nowSeconds: number = Date.now() / 1000): boolean {
  const exp = decodeExpirySeconds(token);
  if (exp === null) return true;
  return exp - nowSeconds <= REFRESH_SKEW_SECONDS;
}

/**
 * Calls Keycloak's token endpoint with `grant_type=refresh_token` - the
 * same endpoint src/app/api/auth/callback/route.ts already calls with
 * `grant_type=authorization_code`. Returns null on any failure (network
 * error, or Keycloak rejecting the refresh token because the idle/session
 * timeout has actually elapsed - see the constants above) - the caller's
 * job in that case is the terminal path: clear both cookies and let the
 * normal unauthenticated flow (already handled by every page/route today)
 * redirect to a real login, not to retry or loop.
 */
export async function refreshAccessToken(
  issuer: string,
  clientId: string,
  refreshToken: string,
): Promise<KeycloakTokenResponse | null> {
  try {
    const res = await fetch(`${issuer.replace(/\/$/, "")}/protocol/openid-connect/token`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        grant_type: "refresh_token",
        client_id: clientId,
        refresh_token: refreshToken,
      }),
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as KeycloakTokenResponse;
  } catch {
    return null;
  }
}
