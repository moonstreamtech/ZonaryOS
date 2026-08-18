// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Server-side (token-based) fetch helpers for internal/firm: item 4's
// original name-only mutation, extended by Open Points item 36 to also
// read/write a small set of broader firm-metadata fields (address,
// taxId, defaultLocale, defaultCurrency, logoUrl - all optional, all
// storage-only, see internal/firm/firm.go's doc comment). Mirrors
// lib/workflow.ts's createInstance/executeTransition failure-surfacing
// convention: the settings page needs to show the caller why an update
// didn't go through (e.g. 403 not-owner, 400 field-too-long), not just
// that it failed.

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

// FirmMetadata is GET/PATCH /api/firms/{firmId}'s response shape
// (internal/firm/handlers.go's firmResponse) as-is - a metadata field
// that was never set comes back null, not omitted, so the settings page
// can render an explicit empty state per field rather than guessing.
export type FirmMetadata = {
  firmId: string;
  name: string;
  address: string | null;
  taxId: string | null;
  defaultLocale: string | null;
  defaultCurrency: string | null;
  logoUrl: string | null;
  // vatNumberLabel/taxIdWarning/fiscalYearStartMonth (multi-language UI/
  // localization depth/fiscal compliance batch, Part 3/4): best-effort
  // fiscal hints internal/firm/handlers.go derives from defaultLocale via
  // a narrow, provisional locale->country mapping - all undefined
  // (omitted from the JSON) when no country can be inferred, or when
  // that country hasn't been seeded into fiscal_country_configs. Never
  // persisted, never required for the form to submit.
  vatNumberLabel?: string;
  taxIdWarning?: string;
  fiscalYearStartMonth?: number;
};

/**
 * Calls the Go backend's `GET /api/firms/{firmId}` (item 36's read
 * counterpart to the PATCH below, member-gated - see
 * internal/firm.Get's doc comment). Returns null on any failure (missing/
 * expired token, non-member, backend unreachable), same convention as
 * lib/me.ts's fetchMe.
 */
export async function fetchFirmMetadata(
  token: string,
  firmId: string,
): Promise<FirmMetadata | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as FirmMetadata;
  } catch {
    return null;
  }
}

// UpdateFirmFields is updateFirm's optional-metadata input: undefined
// means "leave this field alone" (the request key is omitted entirely,
// same as internal/firm.UpdateFields's nil pointer meaning), an empty
// string means "clear it back to unset", and a non-empty string sets it -
// matching the backend's own three-way distinction.
export type UpdateFirmFields = {
  address?: string;
  taxId?: string;
  defaultLocale?: string;
  defaultCurrency?: string;
  logoUrl?: string;
};

export async function updateFirm(
  token: string,
  firmId: string,
  name: string,
  fields: UpdateFirmFields = {},
): Promise<
  { ok: true; metadata: FirmMetadata } | { ok: false; error: string; status: number }
> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}`, {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ name, ...fields }),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return {
        ok: false,
        error: text || `Request failed (${res.status})`,
        status: res.status,
      };
    }
    const metadata = (await res.json()) as FirmMetadata;
    return { ok: true, metadata };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
