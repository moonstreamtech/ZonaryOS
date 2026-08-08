// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Invoicing, payment tracking, and receivables management, frontend
// side: invoices/lines/payments/aging (internal/invoicing) - mirrors
// lib/crm.ts's/lib/logistics.ts's own "types + fetch helpers, one file
// per backend package" convention.

export type InvoiceStatus = "draft" | "sent" | "paid" | "overdue" | "cancelled";

export type InvoiceLine = {
  id: string;
  description: string;
  quantity: string;
  unitPrice: string;
  taxRate: string;
  lineTotal: string;
  productId?: string;
};

export type Invoice = {
  id: string;
  invoiceNumber: string;
  customerId?: string;
  issuedDate: string;
  dueDate?: string;
  status: InvoiceStatus;
  subtotal: string;
  taxAmount: string;
  total: string;
  currency: string;
  notes?: string;
  sourceWorkflowInstance?: string;
  createdAt: string;
  lines?: InvoiceLine[];
};

export type Payment = {
  id: string;
  invoiceId?: string;
  amount: string;
  currency: string;
  paidAt: string;
  method?: string;
  reference?: string;
  createdAt: string;
};

export type AgingBucket = {
  label: string;
  count: number;
  outstanding: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/invoices`. */
export async function fetchInvoices(
  token: string,
  firmId: string,
  opts?: { status?: InvoiceStatus },
): Promise<Invoice[] | null> {
  try {
    const query = opts?.status ? `?status=${encodeURIComponent(opts.status)}` : "";
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invoices${query}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Invoice[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/invoices/{invoiceId}` (with lines). */
export async function fetchInvoice(token: string, firmId: string, invoiceId: string): Promise<Invoice | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invoices/${encodeURIComponent(invoiceId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Invoice;
  } catch {
    return null;
  }
}

export type InvoiceLineInput = {
  description: string;
  quantity: string;
  unitPrice: string;
  taxRate?: string;
  productId?: string;
};

export type CreateInvoiceInput = {
  customerId?: string;
  issuedDate: string;
  dueDate?: string;
  currency?: string;
  notes?: string;
  lines: InvoiceLineInput[];
};

type ApiResult<T extends string, V> = { ok: true } & Record<T, V> | { ok: false; error: string; status: number };

async function postJSON<T extends string, V>(
  url: string,
  method: "POST" | "PATCH" | "DELETE",
  token: string,
  key: T,
  body?: unknown,
): Promise<ApiResult<T, V>> {
  try {
    const res = await fetch(url, {
      method,
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    const value = (await res.json()) as V;
    return { ok: true, [key]: value } as ApiResult<T, V>;
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/invoices` (owner-only). */
export async function createInvoice(
  token: string,
  firmId: string,
  input: CreateInvoiceInput,
): Promise<ApiResult<"invoice", Invoice>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invoices`, "POST", token, "invoice", input);
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/invoices/{invoiceId}` (owner-only). */
export async function updateInvoiceStatus(
  token: string,
  firmId: string,
  invoiceId: string,
  status: InvoiceStatus,
): Promise<ApiResult<"invoice", Invoice>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invoices/${encodeURIComponent(invoiceId)}`,
    "PATCH",
    token,
    "invoice",
    { status },
  );
}

/** Calls the Go backend's `POST /api/firms/{firmId}/invoices/{invoiceId}/lines` (owner-only). */
export async function addInvoiceLine(
  token: string,
  firmId: string,
  invoiceId: string,
  input: InvoiceLineInput,
): Promise<ApiResult<"invoice", Invoice>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invoices/${encodeURIComponent(invoiceId)}/lines`,
    "POST",
    token,
    "invoice",
    input,
  );
}

/** Calls the Go backend's `DELETE /api/firms/{firmId}/invoices/{invoiceId}/lines/{lineId}` (owner-only). */
export async function deleteInvoiceLine(
  token: string,
  firmId: string,
  invoiceId: string,
  lineId: string,
): Promise<ApiResult<"invoice", Invoice>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invoices/${encodeURIComponent(invoiceId)}/lines/${encodeURIComponent(lineId)}`,
    "DELETE",
    token,
    "invoice",
  );
}

export type RecordPaymentInput = {
  amount: string;
  currency?: string;
  paidAt: string; // RFC3339
  method?: string;
  reference?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/invoices/{invoiceId}/payments` (owner-only). */
export async function recordPayment(
  token: string,
  firmId: string,
  invoiceId: string,
  input: RecordPaymentInput,
): Promise<ApiResult<"result", { payment: Payment; invoice: Invoice }>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invoices/${encodeURIComponent(invoiceId)}/payments`,
    "POST",
    token,
    "result",
    input,
  );
}

/** Calls the Go backend's `GET /api/firms/{firmId}/invoices/{invoiceId}/payments`. */
export async function fetchPayments(token: string, firmId: string, invoiceId: string): Promise<Payment[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invoices/${encodeURIComponent(invoiceId)}/payments`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Payment[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/reports/receivables-aging`. */
export async function fetchReceivablesAging(token: string, firmId: string): Promise<AgingBucket[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/reports/receivables-aging`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as AgingBucket[];
  } catch {
    return null;
  }
}
