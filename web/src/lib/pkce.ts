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
