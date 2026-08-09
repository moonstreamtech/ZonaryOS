// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Approval workflow, frontend side (internal/workflow's pending_approvals,
// Open Points item 41) - mirrors lib/edgeAgents.ts's own "types + fetch
// helpers, one file per backend package" convention.

export type ApprovalStatus = "pending" | "approved" | "rejected" | "expired";

export type PendingApproval = {
  id: string;
  instanceId: string;
  proposedActionKey: string;
  permissionKey: string;
  requestedBy: string;
  ruleId?: string;
  status: ApprovalStatus;
  resolvedBy?: string;
  resolvedAt?: string;
  expiresAt?: string;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/pending-approvals`. */
export async function fetchPendingApprovals(token: string, firmId: string): Promise<PendingApproval[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/pending-approvals`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as PendingApproval[];
  } catch {
    return null;
  }
}

async function resolveApproval(
  token: string,
  firmId: string,
  approvalId: string,
  action: "approve" | "reject",
): Promise<{ ok: true; approval: PendingApproval } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/pending-approvals/${encodeURIComponent(approvalId)}/${action}`,
      { method: "POST", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, approval: (await res.json()) as PendingApproval };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `POST .../pending-approvals/{id}/approve`. */
export async function approveApproval(token: string, firmId: string, approvalId: string) {
  return resolveApproval(token, firmId, approvalId, "approve");
}

/** Calls the Go backend's `POST .../pending-approvals/{id}/reject`. */
export async function rejectApproval(token: string, firmId: string, approvalId: string) {
  return resolveApproval(token, firmId, approvalId, "reject");
}
