export type MeFirm = {
  firmId: string;
  firmName: string;
  roleId: string;
};

export type MeResponse = {
  subject: string;
  email: string;
  displayName: string;
  firms: MeFirm[];
};

export type RoleInFirm = {
  firmId: string;
  roleId: string;
  roleKey: string;
  roleName: string;
  isOwner: boolean;
};

/**
 * Calls the Go backend's `/api/me` with token as a Bearer token. Returns
 * null on any failure (missing/expired token, backend unreachable) rather
 * than throwing - callers render an unauthenticated view in that case.
 */
export async function fetchMe(token: string): Promise<MeResponse | null> {
  const apiBase = process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
  try {
    const res = await fetch(`${apiBase}/api/me`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as MeResponse;
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/me/firms/{firmId}/role`. Used
 * server-side (see the root layout) to decide whether to show the
 * Permission Audit Mode toggle at all - only isOwner roles get it (see
 * docs/DEVELOPMENT.md's "who can use it" note). Returns null on any
 * failure, same convention as fetchMe.
 */
export async function fetchRoleInFirm(
  token: string,
  firmId: string,
): Promise<RoleInFirm | null> {
  const apiBase = process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
  try {
    const res = await fetch(
      `${apiBase}/api/me/firms/${encodeURIComponent(firmId)}/role`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as RoleInFirm;
  } catch {
    return null;
  }
}
