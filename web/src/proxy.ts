// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextResponse, type NextRequest } from "next/server";
import createMiddleware from "next-intl/middleware";
import { routing } from "@/i18n/routing";
import {
  REFRESH_COOKIE,
  SESSION_COOKIE,
  isNearExpiry,
  refreshAccessToken,
  sessionCookieOptions,
} from "@/lib/session";

const isProduction = process.env.NODE_ENV === "production";
const intlMiddleware = createMiddleware(routing);

// Session refresh (docs/OPEN_POINTS.md item 41 / docs/DEVELOPMENT.md's
// "Session refresh" section) lives here, in the proxy (Next.js's renamed
// middleware, see next.config/proxy.ts's own history), rather than as a
// wrapper around the per-endpoint fetch helpers in src/lib/*.ts. Two
// reasons, in order of how load-bearing they are:
//
// 1. React Server Components (every page under src/app/[locale] that
//    calls requireFirmContext -> fetchMe) read cookies() but cannot
//    *write* them during render - Next.js throws if a Server Component
//    tries. Only a Route Handler, Server Action, or the proxy itself can
//    set a Set-Cookie header. A fetch-wrapper approach could refresh the
//    token from inside a Route Handler (which reads zonaryos_session
//    directly off `request.cookies` today - see every file under
//    src/app/api/*/route.ts), but it structurally could not do the same
//    for a Server Component page, since fetchMe/fetchDefinitionByKey/etc.
//    are called mid-render there, not from a Route Handler. The proxy
//    runs *before* both, so it's the one place that can refresh once and
//    have both page renders and API routes see the new token.
// 2. Consistency: 17 of the src/app/api/*/route.ts handlers already read
//    `request.cookies.get("zonaryos_session")?.value` directly (per a
//    repo-wide grep) rather than going through a
//    shared helper - there is no existing single chokepoint on the fetch
//    side to retrofit. Doing the refresh once here, before any of those
//    handlers or pages run, means zero changes were needed to any of
//    src/lib/*.ts's ~20 fetch helpers or the route/page files that call
//    them - they keep reading a plain, already-valid (or already-cleared)
//    cookie exactly as they did before this fix.
//
// The matcher below intentionally covers both page and /api routes (next-
// intl's own default matcher excludes /api - see the export at the
// bottom), except the auth routes themselves: refreshing mid-login-flow
// or mid-logout would be pointless (login/callback don't have a session
// cookie yet; logout is actively destroying it) and callback's PKCE
// cookies aren't the ones this logic touches anyway.
const AUTH_PATH_PREFIX = "/api/auth/";

export default async function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;
  const isApiPath = pathname.startsWith("/api/");

  if (pathname.startsWith(AUTH_PATH_PREFIX)) {
    return NextResponse.next();
  }

  const accessToken = request.cookies.get(SESSION_COOKIE)?.value;
  const refreshToken = request.cookies.get(REFRESH_COOKIE)?.value;

  let refreshedAccessToken: string | undefined;
  let refreshedRefreshToken: string | undefined;
  let refreshedAccessMaxAge = 300;
  let refreshedRefreshMaxAge = 1800;
  let terminal = false;

  if (accessToken && refreshToken && isNearExpiry(accessToken)) {
    const issuer = process.env.ZONARYOS_KEYCLOAK_ISSUER_URL;
    const clientId = process.env.ZONARYOS_KEYCLOAK_CLIENT_ID;
    if (issuer && clientId) {
      const tokens = await refreshAccessToken(issuer, clientId, refreshToken);
      if (tokens) {
        refreshedAccessToken = tokens.access_token;
        refreshedRefreshToken = tokens.refresh_token ?? refreshToken;
        refreshedAccessMaxAge = tokens.expires_in ?? 300;
        refreshedRefreshMaxAge = tokens.refresh_expires_in ?? 1800;
      } else {
        // Keycloak rejected the refresh_token grant outright (not a
        // network hiccup - refreshAccessToken already returns null for
        // those too, which is the conservative choice: at worst this
        // costs one extra login on a transient network blip, which is
        // far less confusing than looping or silently proceeding with a
        // token the backend is about to reject anyway). Most likely
        // cause given this realm's config: the ~30 minute SSO session
        // idle timeout actually elapsed. This is the terminal case the
        // task calls out - a real re-login prompt IS correct here, not a
        // bug - handled by clearing both cookies below and letting each
        // page/route's own existing "no valid session" path (already
        // redirect-to-login for pages, already 401 for API routes) do
        // the rest, unchanged.
        terminal = true;
      }
    }
  } else if (!accessToken && refreshToken) {
    // Access token cookie already expired/absent (e.g. its own maxAge
    // elapsed, or this is a very old cookie from before this fix) but the
    // refresh token cookie is still present and its own maxAge hasn't
    // elapsed - same silent-refresh path.
    const issuer = process.env.ZONARYOS_KEYCLOAK_ISSUER_URL;
    const clientId = process.env.ZONARYOS_KEYCLOAK_CLIENT_ID;
    if (issuer && clientId) {
      const tokens = await refreshAccessToken(issuer, clientId, refreshToken);
      if (tokens) {
        refreshedAccessToken = tokens.access_token;
        refreshedRefreshToken = tokens.refresh_token ?? refreshToken;
        refreshedAccessMaxAge = tokens.expires_in ?? 300;
        refreshedRefreshMaxAge = tokens.refresh_expires_in ?? 1800;
      } else {
        terminal = true;
      }
    }
  } else if (accessToken && isNearExpiry(accessToken) && !refreshToken) {
    // Access token is expiring/expired and there is no refresh token to
    // fall back on (pre-fix cookie, or it already expired on its own).
    // Nothing to refresh with - let it lapse into the same "unauthenticated"
    // path every page/route already handles.
    terminal = true;
  }

  // Rewrite the *request's* cookies (not just the eventual response's) so
  // that anything downstream in this same request - a Server Component's
  // cookies() read, or a Route Handler's request.cookies.get() - sees the
  // refreshed value immediately, without waiting for a second round trip
  // after the browser receives the new Set-Cookie header.
  if (refreshedAccessToken) {
    request.cookies.set(SESSION_COOKIE, refreshedAccessToken);
  }
  if (refreshedRefreshToken) {
    request.cookies.set(REFRESH_COOKIE, refreshedRefreshToken);
  }
  if (terminal) {
    request.cookies.delete(SESSION_COOKIE);
    request.cookies.delete(REFRESH_COOKIE);
  }

  const response = isApiPath
    ? NextResponse.next({ request })
    : intlMiddleware(request);

  if (refreshedAccessToken) {
    response.cookies.set(
      SESSION_COOKIE,
      refreshedAccessToken,
      sessionCookieOptions(isProduction, refreshedAccessMaxAge),
    );
  }
  if (refreshedRefreshToken) {
    response.cookies.set(
      REFRESH_COOKIE,
      refreshedRefreshToken,
      sessionCookieOptions(isProduction, refreshedRefreshMaxAge),
    );
  }
  if (terminal) {
    response.cookies.delete(SESSION_COOKIE);
    response.cookies.delete(REFRESH_COOKIE);
  }

  return response;
}

export const config = {
  matcher: ["/((?!_next|_vercel|.*\\..*).*)"],
};
