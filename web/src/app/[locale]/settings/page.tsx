// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations, setRequestLocale } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchDefinitions } from "@/lib/workflow";
import FirmNameEditor from "./FirmNameEditor";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Item 3 (extended by item 4): a real firm settings page - application
// settings, not build config. The `firms` table
// (migrations/0001_core_schema.up.sql) has `name`/`attributes`/
// `created_at`; only `name` is mutable today (internal/firm.UpdateName,
// item 4 - a deliberately narrow, owner-gated PATCH, not a general firm-
// settings endpoint). `attributes` still has no HTTP handler anywhere
// touching it, so this page still shows everything else read-only rather
// than promising editing the backend can't back - see the new Open
// Points entry for the broader firm-metadata story this doesn't attempt.
export default async function SettingsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Settings");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const definitions = await fetchDefinitions(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("title")}
      </h1>

      <div className="w-full max-w-md rounded-lg border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-950">
        <dl className="flex flex-col gap-3 text-sm">
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("firmNameLabel")}</dt>
            {role?.isOwner ? (
              <FirmNameEditor firmId={firm.firmId} currentName={firm.firmName} />
            ) : (
              <dd className="text-black dark:text-zinc-50">{firm.firmName}</dd>
            )}
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("roleLabel")}</dt>
            <dd className="text-black dark:text-zinc-50">
              {role?.roleName ?? t("roleUnavailable")}
            </dd>
          </div>
        </dl>
      </div>

      <div className="w-full max-w-md">
        <h2 className="mb-3 text-lg font-semibold text-black dark:text-zinc-50">
          {t("workflowsTitle")}
        </h2>
        {definitions === null ? (
          <p className="text-red-600 dark:text-red-400">{t("workflowsLoadError")}</p>
        ) : definitions.length === 0 ? (
          <p className="text-zinc-600 dark:text-zinc-400">{t("workflowsEmpty")}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {definitions.map((definition) => (
              <li
                key={definition.definitionId}
                className="rounded-lg border border-zinc-200 bg-white px-4 py-2 text-sm text-black dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-50"
              >
                {definition.name}
              </li>
            ))}
          </ul>
        )}
      </div>

      <p className="max-w-md text-center text-xs text-zinc-500 dark:text-zinc-500">
        {t("editingNotAvailable")}
      </p>
    </main>
  );
}
