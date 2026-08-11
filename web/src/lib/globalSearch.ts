// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Cross-entity search (Part 1 of the search/filtering/enrichment batch,
// internal/search) - types + fetch helper, one file per backend package,
// same convention as lib/apikeys.ts/lib/webhooks.ts.

export type SearchHit = {
  type: "products" | "customers" | "people" | "suppliers";
  id: string;
  title: string;
  subtitle?: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/search?q=&types=`. */
export async function searchEntities(token: string, firmId: string, q: string): Promise<SearchHit[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/search?q=${encodeURIComponent(q)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as SearchHit[];
  } catch {
    return null;
  }
}
