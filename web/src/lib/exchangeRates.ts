// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Multi-currency foundation, frontend side (internal/currency) - mirrors
// lib/platformAdmin.ts's own "returns null on any failure, page treats
// that as notFound()" convention for the platform-admin-gated write path.

export type ExchangeRate = {
  id: string;
  fromCurrency: string;
  toCurrency: string;
  rate: string;
  validFrom: string;
  validTo?: string;
  source?: string;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/exchange-rates` - deliberately
 * unauthenticated (no token needed or sent): exchange rates carry no
 * sensitive data.
 */
export async function fetchExchangeRates(): Promise<ExchangeRate[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/exchange-rates`, { cache: "no-store" });
    if (!res.ok) return null;
    return (await res.json()) as ExchangeRate[];
  } catch {
    return null;
  }
}

export type CreateExchangeRateInput = {
  fromCurrency: string;
  toCurrency: string;
  rate: string;
  validFrom: string;
  source?: string;
};

/**
 * Calls the Go backend's `POST /api/exchange-rates`
 * (internal/platformadmin.Allowlist-gated, same posture as
 * lib/platformAdmin.ts's own fetchPlatformAdminFirms - a non-2xx
 * response, including the 404 a non-allowlisted caller gets, is not
 * distinguished from any other failure here).
 */
export async function createExchangeRate(
  token: string,
  input: CreateExchangeRateInput,
): Promise<{ ok: true; rate: ExchangeRate } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/exchange-rates`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      return { ok: false, status: res.status };
    }
    return { ok: true, rate: (await res.json()) as ExchangeRate };
  } catch {
    return { ok: false, status: 0 };
  }
}
