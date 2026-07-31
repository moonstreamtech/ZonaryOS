// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

export type RoleGrant = {
  roleId: string;
  roleKey: string;
  roleName: string;
  permissionKeys: string[];
};

export type FirmPermissionAudit = {
  myPermissionKeys: string[];
  roles: RoleGrant[];
};

export type PermissionCatalogEntry = {
  key: string;
  description: string;
  roleKeys: string[];
};

export type FirmPermissionCatalog = {
  entries: PermissionCatalogEntry[];
};

export type FirmMember = {
  userId: string;
  email: string;
  displayName: string;
  roleId: string;
  roleKey: string;
  roleName: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/permission-audit`
 * (owner-only). Used by the Next.js proxy route, not directly by client
 * components - mirrors lib/workflow.ts's fetch helpers. Returns null on
 * any failure (including a 403 for a non-owner, or a 404 for a
 * non-member) rather than throwing.
 */
export async function fetchFirmPermissionAudit(
  token: string,
  firmId: string,
): Promise<FirmPermissionAudit | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/permission-audit`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as FirmPermissionAudit;
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/permission-catalog`
 * (owner-only) - the full permission-management page's data source
 * (`/settings/permissions`): every permission key that exists for the
 * firm, across every workflow definition, and which roles hold each -
 * including a key nobody's been granted yet, which
 * fetchFirmPermissionAudit's role-keyed shape can't represent. Same
 * null-on-failure convention as fetchFirmPermissionAudit.
 */
export async function fetchFirmPermissionCatalog(
  token: string,
  firmId: string,
): Promise<FirmPermissionCatalog | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/permission-catalog`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as FirmPermissionCatalog;
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/members` - item 3's
 * firm roster (who's in the firm, and which role they hold).
 * Membership-gated only, not owner-only - see the backend's
 * ListMembers/handleListMembers doc comments. Same null-on-failure
 * convention as the other read helpers above.
 */
export async function fetchFirmMembers(
  token: string,
  firmId: string,
): Promise<FirmMember[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/members`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as FirmMember[];
  } catch {
    return null;
  }
}

type MutationResult = { ok: true } | { ok: false; error: string; status: number };

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/roles` - item 5's
 * role-creation mechanism. Same failure-surfacing convention as
 * grantPermission/revokePermission below (not the swallow-to-null read
 * helpers above): a form submitting this needs to show the caller why it
 * didn't go through (e.g. 409 duplicate key).
 */
export async function createRole(
  token: string,
  firmId: string,
  key: string,
  name: string,
): Promise<
  | { ok: true; roleId: string }
  | { ok: false; error: string; status: number }
> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/roles`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify({ key, name }),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    const body = (await res.json()) as { roleId: string };
    return { ok: true, roleId: body.roleId };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/roles/{roleId}` - renames a role. */
export async function renameRole(
  token: string,
  firmId: string,
  roleId: string,
  name: string,
): Promise<MutationResult> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/roles/${encodeURIComponent(roleId)}`,
      {
        method: "PATCH",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
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

/** Calls the Go backend's `DELETE /api/firms/{firmId}/roles/{roleId}` - deletes a role. */
export async function deleteRole(
  token: string,
  firmId: string,
  roleId: string,
): Promise<MutationResult> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/roles/${encodeURIComponent(roleId)}`,
      {
        method: "DELETE",
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

async function setGrant(
  method: "PUT" | "DELETE",
  token: string,
  firmId: string,
  roleId: string,
  permissionKey: string,
): Promise<MutationResult> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/roles/${encodeURIComponent(roleId)}/permissions/${encodeURIComponent(permissionKey)}`,
      {
        method,
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

/** Calls the Go backend's `PUT .../roles/{roleId}/permissions/{key}`. */
export function grantPermission(
  token: string,
  firmId: string,
  roleId: string,
  permissionKey: string,
): Promise<MutationResult> {
  return setGrant("PUT", token, firmId, roleId, permissionKey);
}

/** Calls the Go backend's `DELETE .../roles/{roleId}/permissions/{key}`. */
export function revokePermission(
  token: string,
  firmId: string,
  roleId: string,
  permissionKey: string,
): Promise<MutationResult> {
  return setGrant("DELETE", token, firmId, roleId, permissionKey);
}
