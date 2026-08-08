// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Logistics management, frontend side: deliveries
// (internal/logistics.Delivery) - mirrors lib/inventory.ts's own "types +
// fetch helpers, one file per backend package" convention.

export type DeliveryStatus = "pending" | "in_transit" | "delivered" | "returned" | "cancelled";

export type Delivery = {
  id: string;
  reference?: string;
  originAddress?: string;
  destinationAddress?: string;
  carrier?: string;
  trackingNumber?: string;
  status: DeliveryStatus;
  estimatedDate?: string;
  actualDate?: string;
  sourceType?: string;
  sourceId?: string;
  customFields: Record<string, unknown>;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/deliveries`. */
export async function fetchDeliveries(
  token: string,
  firmId: string,
  opts: { status?: DeliveryStatus } = {},
): Promise<Delivery[] | null> {
  try {
    const params = new URLSearchParams();
    if (opts.status) params.set("status", opts.status);
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/deliveries${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Delivery[];
  } catch {
    return null;
  }
}

export type CreateDeliveryInput = {
  reference?: string;
  originAddress?: string;
  destinationAddress?: string;
  carrier?: string;
  trackingNumber?: string;
  estimatedDate?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/deliveries` (owner-only). */
export async function createDelivery(
  token: string,
  firmId: string,
  input: CreateDeliveryInput,
): Promise<{ ok: true; delivery: Delivery } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/deliveries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, delivery: (await res.json()) as Delivery };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

export type UpdateDeliveryInput = Partial<{
  reference: string;
  originAddress: string;
  destinationAddress: string;
  carrier: string;
  trackingNumber: string;
  status: DeliveryStatus;
  estimatedDate: string;
  actualDate: string;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/deliveries/{deliveryId}` (owner-only). */
export async function updateDelivery(
  token: string,
  firmId: string,
  deliveryId: string,
  patch: UpdateDeliveryInput,
): Promise<{ ok: true; delivery: Delivery } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/deliveries/${encodeURIComponent(deliveryId)}`,
      {
        method: "PATCH",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(patch),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, delivery: (await res.json()) as Delivery };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
