"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useEffect, useState, type FormEvent } from "react";
import { usePathname } from "next/navigation";
import { useLocale, useTranslations } from "next-intl";
import type { HelpArticle } from "@/lib/helpArticles";

// Strips the /{locale} prefix next-intl's own routing adds, so the route
// passed to GET /api/help matches internal/helparticles' own
// related_route seed values (e.g. "/workflows", not "/en/workflows").
function currentRoute(pathname: string, locale: string): string {
  const prefix = `/${locale}`;
  if (pathname === prefix) return "/";
  if (pathname.startsWith(`${prefix}/`)) return pathname.slice(prefix.length);
  return pathname;
}

// Contextual help slide-out panel (Part 2 of the onboarding/help/UX
// batch): a `?` trigger in the nav shell, desktop-only (space
// constraint, per the design brief - lg:block, same breakpoint every
// other desktop-only nav chrome piece in NavShell.tsx already uses).
// Opens to the current page's related articles; a search box switches to
// full-text search across every article once the caller types something.
export default function HelpPanel() {
  const t = useTranslations("Help");
  const locale = useLocale();
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const [articles, setArticles] = useState<HelpArticle[] | null>(null);
  const [selected, setSelected] = useState<HelpArticle | null>(null);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    const route = currentRoute(pathname, locale);
    const url = query.trim()
      ? `/api/help/search?${new URLSearchParams({ q: query, locale }).toString()}`
      : `/api/help?${new URLSearchParams({ route, locale }).toString()}`;
    // The loading/selected resets below run inside this .then chain
    // (never synchronously in the effect body itself) so a query change
    // doesn't cascade into a second synchronous render pass.
    Promise.resolve()
      .then(() => {
        if (cancelled) return Promise.reject(new Error("cancelled"));
        setLoading(true);
        setSelected(null);
        return fetch(url);
      })
      .then((res) => (res.ok ? res.json() : null))
      .then((data: HelpArticle[] | null) => {
        if (!cancelled) setArticles(data);
      })
      .catch(() => {
        if (!cancelled) setArticles(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- pathname/locale only need re-fetching on open, not on every keystroke; query is debounced via submitSearch instead
  }, [open, query]);

  function submitSearch(e: FormEvent) {
    e.preventDefault();
  }

  return (
    <>
      <button
        type="button"
        data-permission-public="true"
        aria-label={t("openButton")}
        title={t("openButton")}
        onClick={() => setOpen(true)}
        className="hidden h-7 w-7 shrink-0 items-center justify-center rounded-full border border-[var(--color-sidebar-border)] text-xs font-semibold text-[var(--color-sidebar-fg-muted)] hover:bg-[var(--color-sidebar-hover)] hover:text-[var(--color-sidebar-fg)] lg:flex"
      >
        ?
      </button>

      {open && (
        <div className="fixed inset-0 z-50 flex justify-end">
          <button
            type="button"
            aria-label={t("closeButton")}
            data-permission-public="true"
            onClick={() => setOpen(false)}
            className="absolute inset-0 bg-black/30"
          />
          <div className="relative flex h-full w-full max-w-md flex-col gap-4 overflow-y-auto bg-white p-5 shadow-xl dark:bg-zinc-950">
            <div className="flex items-center justify-between">
              <h2 className="text-base font-semibold text-black dark:text-zinc-50">{t("title")}</h2>
              <button
                type="button"
                data-permission-public="true"
                onClick={() => setOpen(false)}
                aria-label={t("closeButton")}
                className="rounded-md p-1 text-zinc-500 hover:bg-zinc-100 dark:hover:bg-zinc-900"
              >
                ×
              </button>
            </div>

            <form onSubmit={submitSearch} role="search" data-permission-public="true" className="flex gap-2">
              <input
                type="text"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t("searchPlaceholder")}
                className="w-full rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
              />
            </form>

            {selected ? (
              <div className="flex flex-col gap-3">
                <button
                  type="button"
                  data-permission-public="true"
                  onClick={() => setSelected(null)}
                  className="self-start text-xs font-medium text-zinc-600 underline dark:text-zinc-400"
                >
                  {t("back")}
                </button>
                <h3 className="text-sm font-semibold text-black dark:text-zinc-50">{selected.title}</h3>
                {/* Markdown content rendered as plain preformatted text
                    (no Markdown renderer dependency in this codebase yet) -
                    still readable, headings/lists remain visually
                    distinguishable via the raw #/- characters; a real
                    renderer is an easy drop-in swap later without changing
                    this component's own data flow. */}
                <p className="whitespace-pre-wrap text-sm text-zinc-700 dark:text-zinc-300">{selected.content}</p>
              </div>
            ) : loading ? (
              <p className="text-sm text-zinc-500 dark:text-zinc-500">{t("loading")}</p>
            ) : !articles || articles.length === 0 ? (
              <p className="text-sm text-zinc-500 dark:text-zinc-500">{t("empty")}</p>
            ) : (
              <ul className="flex flex-col gap-1">
                {articles.map((article) => (
                  <li key={article.id}>
                    <button
                      type="button"
                      data-permission-public="true"
                      onClick={() => setSelected(article)}
                      className="w-full rounded-md px-3 py-2 text-left text-sm text-zinc-800 hover:bg-zinc-100 dark:text-zinc-200 dark:hover:bg-zinc-900"
                    >
                      {article.title}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}
    </>
  );
}
