// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Webhooks foundation, frontend side (internal/webhook) - mirrors
// lib/apikeys.ts's own "types + fetch helpers, one file per backend
// package" convention.

export const WEBHOOK_EVENTS = [
  "workflow.instance.created",
  "workflow.transition.executed",
  "invoice.created",
  "invoice.paid",
  "approval.pending",
] as const;

export type Webhook = {
  id: string;
  name: string;
  url: string;
  events: string[];
  // Unlike APIKey.key, this is returned on every response (list, create,
  // update) - internal/webhook stores it plaintext (the owner needs to
  // read it back to configure their receiving endpoint's own HMAC
  // verification), see internal/webhook.go's own package doc comment.
  secret: string;
  isActive: boolean;
  lastTriggeredAt?: string;
  createdAt: string;
};

export type WebhookDelivery = {
  id: string;
  eventType: string;
  payload: Record<string, unknown>;
  responseStatus?: number;
  responseBody?: string;
  deliveredAt: string;
  success: boolean;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/webhooks` (owner-only). */
export async function fetchWebhooks(token: string, firmId: string): Promise<Webhook[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/webhooks`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Webhook[];
  } catch {
    return null;
  }
}

export type WebhookInput = {
  name: string;
  url: string;
  events: string[];
  isActive: boolean;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/webhooks` (owner-only). */
export async function createWebhook(
  token: string,
  firmId: string,
  input: WebhookInput,
): Promise<{ ok: true; webhook: Webhook } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/webhooks`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, webhook: (await res.json()) as Webhook };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/webhooks/{webhookId}` (owner-only). */
export async function updateWebhook(
  token: string,
  firmId: string,
  webhookId: string,
  input: WebhookInput,
): Promise<{ ok: true; webhook: Webhook } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/webhooks/${encodeURIComponent(webhookId)}`,
      {
        method: "PATCH",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(input),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, webhook: (await res.json()) as Webhook };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `DELETE /api/firms/{firmId}/webhooks/{webhookId}` (owner-only). */
export async function deleteWebhook(token: string, firmId: string, webhookId: string): Promise<boolean> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/webhooks/${encodeURIComponent(webhookId)}`,
      { method: "DELETE", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    return res.ok;
  } catch {
    return false;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/webhooks/{webhookId}/deliveries` (owner-only). */
export async function fetchWebhookDeliveries(
  token: string,
  firmId: string,
  webhookId: string,
): Promise<WebhookDelivery[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/webhooks/${encodeURIComponent(webhookId)}/deliveries`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as WebhookDelivery[];
  } catch {
    return null;
  }
}
