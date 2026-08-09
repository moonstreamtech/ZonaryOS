// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Vision §3's import/export engine, frontend side: mirrors lib/workflow.ts/
// lib/accounting.ts's own "types + fetch helpers, one file per backend
// package" convention. ExportDocument is treated as an opaque JSON blob
// here (unknown), not a fully-typed mirror of internal/portability.ExportDocument -
// the frontend never inspects its fields, only downloads it (export) or
// re-uploads it unmodified (import), so a full type wouldn't earn its
// keep and would drift the moment the backend's own domain structs change.

export type ImportCounts = {
  imported: number;
  skipped: number;
};

export type ImportSummary = {
  workflowDefinitions: ImportCounts;
  rules: ImportCounts;
  accounts: ImportCounts;
  products: ImportCounts;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/export` (owner-only) and
 * returns the raw response body text - the exact bytes to hand back to
 * the browser for download, unparsed, so this proxy never needs to know
 * ExportDocument's shape.
 */
export async function fetchExport(
  token: string,
  firmId: string,
): Promise<{ ok: true; body: string } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/export`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, body: await res.text() };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/import/configuration`
 * (owner-only) with a raw JSON document body (the exact text an earlier
 * export produced, possibly from a different ZonaryOS instance entirely -
 * see internal/portability's own doc comment on vendor lock-in) and
 * returns the resulting ImportSummary.
 */
export async function importConfiguration(
  token: string,
  firmId: string,
  documentJSON: string,
): Promise<{ ok: true; summary: ImportSummary } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/import/configuration`,
      {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: documentJSON,
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, summary: (await res.json()) as ImportSummary };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
