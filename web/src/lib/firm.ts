// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Server-side (token-based) fetch helper for internal/firm.UpdateName -
// item 4's minimal, bounded firm mutation (name only). Mirrors
// lib/workflow.ts's createInstance/executeTransition failure-surfacing
// convention: the settings page needs to show the caller why a rename
// didn't go through (e.g. 403 not-owner), not just that it failed.

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

export async function updateFirmName(
  token: string,
  firmId: string,
  name: string,
): Promise<{ ok: true; name: string } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}`, {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ name }),
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
    const body = (await res.json()) as { firmId: string; name: string };
    return { ok: true, name: body.name };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
