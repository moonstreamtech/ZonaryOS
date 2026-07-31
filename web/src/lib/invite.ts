// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

export type Invite = {
  inviteId: string;
  roleId: string;
  roleName: string;
  token?: string;
  status: "pending" | "used" | "revoked";
  expired: boolean;
  expiresAt: string;
  createdAt: string;
  usedAt?: string;
};

export type InviteInfo = {
  firmName: string;
  roleName: string;
  status: "pending" | "used" | "revoked";
  expired: boolean;
  expiresAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/invites` (owner-only) -
 * generates a new pending invite for roleId, DefaultExpiry (7 days) out.
 * The returned Invite.token is only ever present on this one response -
 * see internal/invite/handlers.go's toInviteResponse.
 */
export async function createInvite(
  token: string,
  firmId: string,
  roleId: string,
): Promise<{ ok: true; invite: Invite } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invites`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify({ roleId }),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, invite: (await res.json()) as Invite };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/invites` (owner-only) -
 * every invite this firm has ever generated, most recent first. Returns
 * null on any failure (including a 403 for a non-owner), same
 * null-on-failure convention as lib/permissionAudit.ts's read helpers.
 */
export async function fetchInvites(token: string, firmId: string): Promise<Invite[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invites`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Invite[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/invites/{inviteId}/revoke`. */
export async function revokeInvite(
  token: string,
  firmId: string,
  inviteId: string,
): Promise<{ ok: true } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invites/${encodeURIComponent(inviteId)}/revoke`,
      {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/**
 * Calls the Go backend's `GET /api/invites/{token}` - genuinely public, no
 * bearer token, used by the unauthenticated /invite/[token] page to show
 * what the visitor was invited to (or why the link can't be used) before
 * they've signed in at all. Returns null only on a network-level failure;
 * a 404/410/409 still returns a typed result via the `status`/`ok` shape
 * so the page can distinguish "not found" from "expired" from "used" from
 * "revoked" - see internal/invite/handlers.go's writeError.
 */
export async function lookupInvite(
  token: string,
): Promise<
  | { ok: true; info: InviteInfo }
  | { ok: false; httpStatus: number }
  | null
> {
  try {
    const res = await fetch(`${apiBase()}/api/invites/${encodeURIComponent(token)}`, {
      cache: "no-store",
    });
    if (!res.ok) {
      return { ok: false, httpStatus: res.status };
    }
    return { ok: true, info: (await res.json()) as InviteInfo };
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `POST /api/invites/{token}/accept` with the
 * caller's own session token - requires an authenticated ZonaryOS
 * identity, but deliberately not firm membership (see
 * internal/invite/handlers.go's RegisterRoutes doc comment).
 */
export async function acceptInvite(
  sessionToken: string,
  token: string,
): Promise<
  | { ok: true; firmId: string; roleName: string }
  | { ok: false; httpStatus: number }
> {
  try {
    const res = await fetch(`${apiBase()}/api/invites/${encodeURIComponent(token)}/accept`, {
      method: "POST",
      headers: { Authorization: `Bearer ${sessionToken}` },
      cache: "no-store",
    });
    if (!res.ok) {
      return { ok: false, httpStatus: res.status };
    }
    const body = (await res.json()) as { firmId: string; roleId: string; roleName: string };
    return { ok: true, firmId: body.firmId, roleName: body.roleName };
  } catch {
    return { ok: false, httpStatus: 0 };
  }
}
