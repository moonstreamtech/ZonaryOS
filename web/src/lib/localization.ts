// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Address and tax framework foundation, frontend side
// (internal/localization) - mirrors lib/edgeAgents.ts's own "types +
// fetch helpers, one file per backend package" convention.

export type Address = {
  id: string;
  label?: string;
  line1: string;
  line2?: string;
  city: string;
  stateProvince?: string;
  postalCode?: string;
  countryCode: string;
  isDefault: boolean;
  createdAt: string;
};

export type TaxRate = {
  id: string;
  name: string;
  rate: string;
  countryCode?: string;
  region?: string;
  appliesTo?: string;
  isDefault: boolean;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

export async function fetchAddresses(token: string, firmId: string): Promise<Address[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/addresses`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Address[];
  } catch {
    return null;
  }
}

export type AddressInput = Omit<Address, "id" | "createdAt">;

export async function createAddress(
  token: string,
  firmId: string,
  input: AddressInput,
): Promise<{ ok: true; address: Address } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/addresses`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) return { ok: false, status: res.status };
    return { ok: true, address: (await res.json()) as Address };
  } catch {
    return { ok: false, status: 0 };
  }
}

export async function deleteAddress(token: string, firmId: string, addressId: string): Promise<boolean> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/addresses/${encodeURIComponent(addressId)}`, {
      method: "DELETE",
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    return res.ok;
  } catch {
    return false;
  }
}

export async function fetchTaxRates(token: string, firmId: string): Promise<TaxRate[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/tax-rates`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as TaxRate[];
  } catch {
    return null;
  }
}

export type TaxRateInput = Omit<TaxRate, "id" | "createdAt">;

export async function createTaxRate(
  token: string,
  firmId: string,
  input: TaxRateInput,
): Promise<{ ok: true; taxRate: TaxRate } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/tax-rates`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) return { ok: false, status: res.status };
    return { ok: true, taxRate: (await res.json()) as TaxRate };
  } catch {
    return { ok: false, status: 0 };
  }
}

export async function deleteTaxRate(token: string, firmId: string, taxRateId: string): Promise<boolean> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/tax-rates/${encodeURIComponent(taxRateId)}`, {
      method: "DELETE",
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    return res.ok;
  } catch {
    return false;
  }
}
