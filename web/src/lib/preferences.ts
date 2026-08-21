// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

export type Theme = "light" | "dark" | "system";
export type Density = "compact" | "default" | "comfortable";
export type DefaultLocale = "en" | "tr" | "ar";

export type UserPreferences = {
  theme?: Theme;
  density?: Density;
  defaultLocale?: DefaultLocale;
};

const API_BASE = () => process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";

/**
 * Calls the Go backend's `GET /api/me/preferences`. Returns null on any
 * failure, same convention as lib/me.ts's fetchMe. Unlike fetchMe, this
 * is not firm-scoped - it works for any authenticated user regardless of
 * firm membership.
 */
export async function fetchPreferences(token: string): Promise<UserPreferences | null> {
  try {
    const res = await fetch(`${API_BASE()}/api/me/preferences`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as UserPreferences;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `PATCH /api/me/preferences` (partial update). */
export async function patchPreferences(
  token: string,
  patch: UserPreferences,
): Promise<UserPreferences | null> {
  try {
    const res = await fetch(`${API_BASE()}/api/me/preferences`, {
      method: "PATCH",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(patch),
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as UserPreferences;
  } catch {
    return null;
  }
}
