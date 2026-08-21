// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

export type HelpArticle = {
  id: string;
  slug: string;
  title: string;
  content: string;
  relatedRoute?: string;
};

const API_BASE = () => process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";

/**
 * Calls the Go backend's `GET /api/help?route=&locale=` - articles
 * relevant to the caller's current page. Returns null on any failure,
 * same convention as lib/me.ts's fetchMe.
 */
export async function fetchHelpArticles(
  token: string,
  route: string,
  locale: string,
): Promise<HelpArticle[] | null> {
  try {
    const params = new URLSearchParams({ route, locale });
    const res = await fetch(`${API_BASE()}/api/help?${params.toString()}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as HelpArticle[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/help/search?q=&locale=`. */
export async function searchHelpArticles(
  token: string,
  q: string,
  locale: string,
): Promise<HelpArticle[] | null> {
  try {
    const params = new URLSearchParams({ q, locale });
    const res = await fetch(`${API_BASE()}/api/help/search?${params.toString()}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as HelpArticle[];
  } catch {
    return null;
  }
}
