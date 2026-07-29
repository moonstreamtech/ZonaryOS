// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import crypto from "crypto";

// PKCE (RFC 7636) helpers for the Authorization Code flow in
// src/app/api/auth/*. Keeping this as a few lines of standard-library
// crypto avoids pulling in a full auth framework of uncertain
// compatibility with this Next.js version just to prove the login flow
// end-to-end.

export function generateCodeVerifier(): string {
  return crypto.randomBytes(32).toString("base64url");
}

export function codeChallengeFromVerifier(verifier: string): string {
  return crypto.createHash("sha256").update(verifier).digest("base64url");
}

export function generateState(): string {
  return crypto.randomBytes(16).toString("base64url");
}
