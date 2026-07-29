// Client-side (browser) fetch helpers for Audit Mode - calls this app's
// own Next.js proxy routes (never the Go backend directly, and never
// carries a token itself: the httpOnly session cookie travels
// automatically with same-origin fetches). Distinct from
// lib/permissionAudit.ts, which is the server-side, token-based half
// those proxy routes call into - the response shape is identical, so the
// types are shared from there rather than redeclared here.

export type {
  RoleGrant,
  FirmPermissionAudit,
} from "@/lib/permissionAudit";
import type { FirmPermissionAudit } from "@/lib/permissionAudit";

export async function fetchFirmPermissionAuditClient(
  firmId: string,
): Promise<FirmPermissionAudit | null> {
  try {
    const res = await fetch(`/api/permission-audit/${firmId}`, {
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as FirmPermissionAudit;
  } catch {
    return null;
  }
}

async function setGrantClient(
  method: "PUT" | "DELETE",
  firmId: string,
  roleId: string,
  permissionKey: string,
): Promise<boolean> {
  try {
    const res = await fetch(
      `/api/permission-audit/${firmId}/roles/${roleId}/permissions/${encodeURIComponent(permissionKey)}`,
      { method },
    );
    return res.ok;
  } catch {
    return false;
  }
}

export function grantPermissionClient(
  firmId: string,
  roleId: string,
  permissionKey: string,
): Promise<boolean> {
  return setGrantClient("PUT", firmId, roleId, permissionKey);
}

export function revokePermissionClient(
  firmId: string,
  roleId: string,
  permissionKey: string,
): Promise<boolean> {
  return setGrantClient("DELETE", firmId, roleId, permissionKey);
}
