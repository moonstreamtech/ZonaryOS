// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Server-side (token-based) fetch helper for internal/platformadmin's one
// endpoint, mirroring lib/auditlog.ts's/lib/me.ts's fetch-helper shape:
// returns null on any failure (missing/expired token, not on the
// allowlist, backend unreachable) rather than throwing or distinguishing
// those cases - app/[locale]/platform-admin/page.tsx treats null as
// next/navigation's notFound(), the stronger "route doesn't exist at all"
// posture this feature deliberately uses instead of an inline
// "not authorized" message (see that page's doc comment).

export type PlatformAdminFirm = {
  firmId: string;
  name: string;
  createdAt: string;
  workflowDefinitionCount: number;
  memberCount: number;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/platform-admin/firms`
 * (internal/platformadmin.Allowlist-gated). Returns null whenever the
 * caller shouldn't see this feature exist at all - a non-2xx response
 * (including the 404 the backend deliberately returns for a
 * non-allowlisted caller, see internal/platformadmin/handlers.go) or any
 * network failure.
 */
export async function fetchPlatformAdminFirms(
  token: string,
): Promise<PlatformAdminFirm[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/platform-admin/firms`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as PlatformAdminFirm[];
  } catch {
    return null;
  }
}
