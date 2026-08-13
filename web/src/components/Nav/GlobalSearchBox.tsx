"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useTranslations } from "next-intl";
import { useRouter } from "@/i18n/navigation";

type Props = {
  // The current query, when this is rendered on /search itself (see
  // app/[locale]/search/page.tsx) - undefined/empty when it's rendered
  // as the nav shell's entry point instead, with nothing typed yet.
  initialQuery?: string;
  // "default": the light-content-area styling used on /search itself.
  // "sidebar": a compact variant with fixed dark colors (not `dark:`
  // conditional), for the nav shell's always-dark sidebar bottom section
  // (see NavShell.tsx) - the icon-only rail below `lg` doesn't render
  // this at all (a text input doesn't fit 48px), only the `lg`+ full
  // sidebar does.
  variant?: "default" | "sidebar";
};

// Item 3's firm-wide search entry point: a plain text box that navigates
// to /search?q=... (a real, shareable/bookmarkable URL, same convention
// as WorkflowInstanceList's own per-definition search box) rather than
// an in-place dropdown - mounted once in the nav shell so it's reachable
// from every page, and reused as-is on the /search results page itself
// so a caller can refine their query without leaving the page. Uses
// next-intl's own locale-aware useRouter (@/i18n/navigation), not plain
// next/navigation, since this is the one client component in this
// codebase that navigates to a *different* route rather than refreshing
// or rewriting query params on the current one.
export default function GlobalSearchBox({ initialQuery = "", variant = "default" }: Props) {
  const t = useTranslations("Search");
  const router = useRouter();
  const [value, setValue] = useState(initialQuery);

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const trimmed = value.trim();
    router.push(trimmed ? `/search?q=${encodeURIComponent(trimmed)}` : "/search");
  }

  const isSidebar = variant === "sidebar";

  return (
    <form
      onSubmit={handleSubmit}
      role="search"
      data-permission-public="true"
      className={isSidebar ? "flex w-full flex-col gap-1.5" : "flex w-full max-w-md gap-2"}
    >
      <input
        type="text"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder={t("placeholder")}
        aria-label={t("placeholder")}
        data-permission-public="true"
        className={
          isSidebar
            ? "w-full rounded-md border border-[var(--color-sidebar-hover)] bg-[var(--color-sidebar-hover)] px-2.5 py-1.5 text-xs text-[var(--color-sidebar-fg)] placeholder:text-[var(--color-sidebar-fg-muted)]"
            : "w-full rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
        }
      />
      <button
        type="submit"
        data-permission-public="true"
        className={
          isSidebar
            ? "rounded-md bg-[var(--color-sidebar-fg)] px-3 py-1 text-xs font-medium text-[var(--color-sidebar-bg)] transition-colors hover:bg-zinc-200"
            : "rounded-md bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
        }
      >
        {t("button")}
      </button>
    </form>
  );
}
