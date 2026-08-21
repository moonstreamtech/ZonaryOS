"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Density, Theme, UserPreferences } from "@/lib/preferences";

type Props = {
  preferences: UserPreferences;
};

// Part 4 of the onboarding/help/UX batch: theme/density/default-locale,
// user-scoped (not firm-scoped - see internal/identity's own
// preferences.go doc comment), so this editor lives on the settings page
// but is visible/editable regardless of the caller's role in the current
// firm. router.refresh() after a save re-renders the root layout, which
// re-reads preferences server-side and applies the new data-density
// attribute (see app/[locale]/layout.tsx).
export default function PreferencesEditor({ preferences }: Props) {
  const t = useTranslations("Preferences");
  const router = useRouter();
  const [theme, setTheme] = useState<Theme>(preferences.theme ?? "system");
  const [density, setDensity] = useState<Density>(preferences.density ?? "default");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save(next: { theme?: Theme; density?: Density }) {
    setSaving(true);
    setError(null);
    try {
      const res = await fetch("/api/preferences", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      });
      if (!res.ok) {
        setError(t("saveError"));
        return;
      }
      router.refresh();
    } catch {
      setError(t("saveError"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div>
        <label htmlFor="theme-select" className="mb-1 block text-sm text-zinc-500 dark:text-zinc-400">
          {t("themeLabel")}
        </label>
        <select
          id="theme-select"
          data-permission-public="true"
          value={theme}
          disabled={saving}
          onChange={(e) => {
            const next = e.target.value as Theme;
            setTheme(next);
            void save({ theme: next });
          }}
          className="w-full rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
        >
          <option value="system">{t("themeSystem")}</option>
          <option value="light">{t("themeLight")}</option>
          <option value="dark">{t("themeDark")}</option>
        </select>
      </div>

      <div>
        <label htmlFor="density-select" className="mb-1 block text-sm text-zinc-500 dark:text-zinc-400">
          {t("densityLabel")}
        </label>
        <select
          id="density-select"
          data-permission-public="true"
          value={density}
          disabled={saving}
          onChange={(e) => {
            const next = e.target.value as Density;
            setDensity(next);
            void save({ density: next });
          }}
          className="w-full rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
        >
          <option value="compact">{t("densityCompact")}</option>
          <option value="default">{t("densityDefault")}</option>
          <option value="comfortable">{t("densityComfortable")}</option>
        </select>
      </div>
    </div>
  );
}
