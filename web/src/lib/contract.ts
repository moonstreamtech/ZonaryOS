// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Contracts management, document workflows, and legal foundation module,
// frontend side (internal/contracts) - mirrors lib/asset.ts's own
// "types + fetch helpers, one file per backend package, postJSON generic
// helper" convention. startDate/endDate are plain "YYYY-MM-DD" strings,
// matching internal/contracts/handlers.go's own formatDate helper -
// never RFC3339. signedAt is the one field on this entity that genuinely
// IS a full RFC3339 timestamp (see internal/contracts/handlers.go's
// handleUpdateContract, which parses it with time.Parse(time.RFC3339,
// ...)) - this batch only displays it read-only, no UI sets it.

export type ContractType = "customer" | "supplier" | "employment" | "nda" | "service" | "other";
export type ContractStatus = "draft" | "review" | "active" | "expired" | "terminated" | "renewed";
export type CounterpartyType = "customer" | "supplier" | "person" | "other";
export type ContractDocumentType = "original" | "amendment" | "annex" | "correspondence";

export type Contract = {
  id: string;
  referenceNumber: string;
  title: string;
  type: ContractType;
  counterpartyName: string;
  counterpartyId?: string;
  counterpartyType?: CounterpartyType;
  status: ContractStatus;
  value?: string;
  currency: string;
  startDate?: string;
  endDate?: string;
  autoRenewal: boolean;
  renewalNoticeDays?: number;
  signedAt?: string;
  signedBy?: string;
  notes?: string;
  customFields: Record<string, unknown>;
  createdAt: string;
};

export type ContractDocument = {
  id: string;
  contractId: string;
  name: string;
  documentUrl?: string;
  type: ContractDocumentType;
  uploadedAt: string;
  uploadedBy: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/contracts`. */
export async function fetchContracts(token: string, firmId: string): Promise<Contract[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/contracts`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Contract[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/contracts/expiring?days=`.
 * Returns firmId's active, non-auto-renewing contracts whose endDate falls
 * within the next `days` days (defaults to the backend's own default of 30
 * when omitted).
 */
export async function fetchExpiringContracts(
  token: string,
  firmId: string,
  days?: number,
): Promise<Contract[] | null> {
  try {
    const url = new URL(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/contracts/expiring`);
    if (days !== undefined) {
      url.searchParams.set("days", String(days));
    }
    const res = await fetch(url, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Contract[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/contracts/{contractId}`. */
export async function fetchContract(token: string, firmId: string, contractId: string): Promise<Contract | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/contracts/${encodeURIComponent(contractId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Contract;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/contracts/{contractId}/documents`. */
export async function fetchContractDocuments(
  token: string,
  firmId: string,
  contractId: string,
): Promise<ContractDocument[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/contracts/${encodeURIComponent(contractId)}/documents`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as ContractDocument[];
  } catch {
    return null;
  }
}

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
    const value = res.status === 204 ? (undefined as V) : ((await res.json()) as V);
    return { ok: true, [key]: value } as ApiResult<T, V>;
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

export type CreateContractInput = {
  referenceNumber: string;
  title: string;
  type: ContractType;
  counterpartyName: string;
  counterpartyId?: string;
  counterpartyType?: CounterpartyType;
  status?: ContractStatus;
  value?: string;
  currency?: string;
  startDate?: string;
  endDate?: string;
  autoRenewal?: boolean;
  renewalNoticeDays?: number;
  notes?: string;
  customFields?: Record<string, unknown>;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/contracts` (owner-only). */
export async function createContract(
  token: string,
  firmId: string,
  input: CreateContractInput,
): Promise<ApiResult<"contract", Contract>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/contracts`, "POST", token, "contract", input);
}

export type UpdateContractInput = Partial<{
  title: string;
  status: ContractStatus;
  counterpartyId: string;
  hasCounterpartyId: boolean;
  value: string;
  startDate: string;
  endDate: string;
  autoRenewal: boolean;
  renewalNoticeDays: number;
  signedAt: string;
  signedBy: string;
  notes: string;
  customFields: Record<string, unknown>;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/contracts/{contractId}` (owner-only). */
export async function updateContract(
  token: string,
  firmId: string,
  contractId: string,
  patch: UpdateContractInput,
): Promise<ApiResult<"contract", Contract>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/contracts/${encodeURIComponent(contractId)}`,
    "PATCH",
    token,
    "contract",
    patch,
  );
}

export type CreateContractDocumentInput = {
  name: string;
  documentUrl?: string;
  type: ContractDocumentType;
};

/**
 * Calls the Go backend's
 * `POST /api/firms/{firmId}/contracts/{contractId}/documents` (owner-only).
 */
export async function createContractDocument(
  token: string,
  firmId: string,
  contractId: string,
  input: CreateContractDocumentInput,
): Promise<ApiResult<"document", ContractDocument>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/contracts/${encodeURIComponent(contractId)}/documents`,
    "POST",
    token,
    "document",
    input,
  );
}
