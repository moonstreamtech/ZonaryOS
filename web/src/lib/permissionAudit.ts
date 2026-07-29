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

type MutationResult = { ok: true } | { ok: false; error: string; status: number };

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
