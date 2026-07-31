// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Server-side (token-based) fetch helper for Vision §3's Audit Trail
// Infrastructure (internal/auditlog), mirroring lib/workflow.ts's and
// lib/permissionAudit.ts's fetch-helper shape. Entry's fields are taken
// directly from the Go backend's entryResponse (internal/auditlog/handlers.go)
// on purpose - the audit log viewer renders them as-is rather than
// reshaping the data.

export type AuditLogEntry = {
  id: string;
  entityType: string;
  entityId: string;
  action: string;
  changes: Record<string, unknown>;
  userId: string;
  userEmail: string;
  userDisplayName: string;
  occurredAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/audit-log`
 * (internal/auditlog.ReadPermission-gated - the firm's owner role by
 * default, see internal/auditlog.go's doc comment). Returns null on any
 * failure (missing/expired token, non-member, lacks the read permission,
 * backend unreachable), same convention as lib/me.ts's fetchMe.
 */
export async function fetchAuditLog(
  token: string,
  firmId: string,
): Promise<AuditLogEntry[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/audit-log`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as AuditLogEntry[];
  } catch {
    return null;
  }
}

export type AuditLogPage = {
  entries: AuditLogEntry[];
  total: number;
};

export type FetchAuditLogPageOptions = {
  limit: number;
  offset: number;
  // RFC3339 timestamps - forwarded to the backend's ?from=/?to= exactly
  // as given (see internal/auditlog.parseListOptions).
  from?: string;
  to?: string;
  definitionId?: string;
};

/**
 * Calls the Go backend's `GET .../audit-log` with `?limit=`/`?offset=`/
 * `?from=`/`?to=`/`?definitionId=` - the paged, filterable read path the
 * /audit-log page needs, as opposed to fetchAuditLog above (still
 * unpaginated, used by WorkflowHistory's own correlation lookup, which
 * wants every entry as a fallback, not just the current page's - same
 * split as lib/workflow.ts's fetchInstances/fetchInstancesPage). The
 * backend reports the filtered-but-unpaged total via the `X-Total-Count`
 * response header rather than changing the array body shape (see
 * internal/auditlog's handleList doc comment) - read here to compute
 * page count. Returns null on failure, same convention as fetchAuditLog.
 */
export async function fetchAuditLogPage(
  token: string,
  firmId: string,
  opts: FetchAuditLogPageOptions,
): Promise<AuditLogPage | null> {
  try {
    const params = new URLSearchParams({
      limit: String(opts.limit),
      offset: String(opts.offset),
    });
    if (opts.from) params.set("from", opts.from);
    if (opts.to) params.set("to", opts.to);
    if (opts.definitionId) params.set("definitionId", opts.definitionId);
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/audit-log?${params.toString()}`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    const entries = (await res.json()) as AuditLogEntry[];
    const total = Number(res.headers.get("X-Total-Count") ?? entries.length);
    return { entries, total: Number.isFinite(total) ? total : entries.length };
  } catch {
    return null;
  }
}
