// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Notification inbox, frontend side (internal/notification) - mirrors
// lib/edgeAgents.ts's own "types + fetch helpers, one file per backend
// package" convention.

export type Notification = {
  id: string;
  type: string;
  title: string;
  body: string;
  payload: Record<string, unknown>;
  readAt?: string;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/notifications`. */
export async function fetchNotifications(
  token: string,
  firmId: string,
  opts: { unreadOnly?: boolean; limit?: number } = {},
): Promise<Notification[] | null> {
  try {
    const params = new URLSearchParams();
    if (opts.unreadOnly) params.set("unread", "true");
    if (opts.limit) params.set("limit", String(opts.limit));
    const qs = params.toString();
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/notifications${qs ? `?${qs}` : ""}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Notification[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/notifications/unread-count`. */
export async function fetchUnreadCount(token: string, firmId: string): Promise<number | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/notifications/unread-count`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    const data = (await res.json()) as { count: number };
    return data.count;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/notifications/{id}/read`. */
export async function markNotificationRead(token: string, firmId: string, notificationId: string): Promise<boolean> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/notifications/${encodeURIComponent(notificationId)}/read`,
      { method: "PATCH", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    return res.ok;
  } catch {
    return false;
  }
}
