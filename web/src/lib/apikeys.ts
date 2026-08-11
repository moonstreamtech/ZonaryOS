// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// API keys foundation, frontend side (internal/apikey) - mirrors
// lib/edgeAgents.ts's own "types + fetch helpers, one file per backend
// package" convention.

export type APIKey = {
  id: string;
  name: string;
  scopes: string[];
  createdBy: string;
  lastUsedAt?: string;
  expiresAt?: string;
  isActive: boolean;
  createdAt: string;
  // key is only ever present on the single response right after
  // creation (internal/apikey.handleCreateAPIKey) - never on a list
  // response, since only the hash is ever stored server-side afterward.
  key?: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/api-keys` (owner-only). */
export async function fetchAPIKeys(token: string, firmId: string): Promise<APIKey[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/api-keys`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as APIKey[];
  } catch {
    return null;
  }
}

export type CreateAPIKeyInput = {
  name: string;
  scopes: string[];
  expiresAt?: string;
};

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/api-keys` (owner-only).
 * The returned APIKey carries the one-time plaintext `key` - the caller
 * (ApiKeysManager.tsx) is responsible for showing it exactly once and
 * never persisting it anywhere.
 */
export async function createAPIKey(
  token: string,
  firmId: string,
  input: CreateAPIKeyInput,
): Promise<{ ok: true; apiKey: APIKey } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/api-keys`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, apiKey: (await res.json()) as APIKey };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `DELETE /api/firms/{firmId}/api-keys/{keyId}` (owner-only, revokes). */
export async function revokeAPIKey(token: string, firmId: string, keyId: string): Promise<boolean> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/api-keys/${encodeURIComponent(keyId)}`,
      { method: "DELETE", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    return res.ok;
  } catch {
    return false;
  }
}
