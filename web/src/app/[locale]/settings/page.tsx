// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations, setRequestLocale } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchDefinitions } from "@/lib/workflow";
import { fetchFirmMetadata } from "@/lib/firm";
import { fetchFirmGroupForFirm } from "@/lib/consolidation";
import { fetchPreferences } from "@/lib/preferences";
import FirmMetadataEditor from "./FirmMetadataEditor";
import PortabilityPanel from "./PortabilityPanel";
import PreferencesEditor from "./PreferencesEditor";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Item 3 (extended by item 4, further extended by Open Points item 36): a
// real firm settings page - application settings, not build config. The
// `firms` table (migrations/0001_core_schema.up.sql, extended by
// migrations/0006_firm_metadata.up.sql) has `name`/`address`/`tax_id`/
// `default_locale`/`default_currency`/`logo_url`/`attributes`/
// `created_at`; every field but `attributes` is mutable now
// (internal/firm.Update, owner-gated). `attributes` still has no HTTP
// handler anywhere touching it - see docs/OPEN_POINTS.md item 36's
// remaining open questions on what that jsonb column should eventually
// hold.
export default async function SettingsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Settings");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const definitions = await fetchDefinitions(sessionToken, firm.firmId);
  const metadata = await fetchFirmMetadata(sessionToken, firm.firmId);
  const firmGroupResult = await fetchFirmGroupForFirm(sessionToken, firm.firmId);
  const preferences = await fetchPreferences(sessionToken);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("title")}
      </h1>

      <div className="w-full max-w-md rounded-lg border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-950">
        {metadata === null ? (
          <p className="text-red-600 dark:text-red-400">{t("metadataLoadError")}</p>
        ) : (
          <dl className="flex flex-col gap-3 text-sm">
            {role?.isOwner ? (
              <FirmMetadataEditor firmId={firm.firmId} metadata={metadata} />
            ) : (
              <>
                <div>
                  <dt className="text-zinc-500 dark:text-zinc-400">{t("firmNameLabel")}</dt>
                  <dd className="text-black dark:text-zinc-50">{metadata.name}</dd>
                </div>
                <div>
                  <dt className="text-zinc-500 dark:text-zinc-400">{t("addressLabel")}</dt>
                  <dd className="text-black dark:text-zinc-50">
                    {metadata.address || (
                      <span className="text-zinc-400 dark:text-zinc-600">{t("notSet")}</span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt className="text-zinc-500 dark:text-zinc-400">{t("taxIdLabel")}</dt>
                  <dd className="text-black dark:text-zinc-50">
                    {metadata.taxId || (
                      <span className="text-zinc-400 dark:text-zinc-600">{t("notSet")}</span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt className="text-zinc-500 dark:text-zinc-400">{t("defaultLocaleLabel")}</dt>
                  <dd className="text-black dark:text-zinc-50">
                    {metadata.defaultLocale || (
                      <span className="text-zinc-400 dark:text-zinc-600">{t("notSet")}</span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt className="text-zinc-500 dark:text-zinc-400">
                    {t("defaultCurrencyLabel")}
                  </dt>
                  <dd className="text-black dark:text-zinc-50">
                    {metadata.defaultCurrency || (
                      <span className="text-zinc-400 dark:text-zinc-600">{t("notSet")}</span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt className="text-zinc-500 dark:text-zinc-400">{t("logoUrlLabel")}</dt>
                  <dd className="text-black dark:text-zinc-50">
                    {metadata.logoUrl || (
                      <span className="text-zinc-400 dark:text-zinc-600">{t("notSet")}</span>
                    )}
                  </dd>
                </div>
              </>
            )}
            <div>
              <dt className="text-zinc-500 dark:text-zinc-400">{t("roleLabel")}</dt>
              <dd className="text-black dark:text-zinc-50">
                {role?.roleName ?? t("roleUnavailable")}
              </dd>
            </div>
          </dl>
        )}
      </div>

      {/* Item 3 of the multi-company consolidation batch
          (internal/consolidation.GetFirmGroupForFirm): a small,
          read-only "which firm group am I in" display, member-visible
          (not owner-gated - this is ordinary firm metadata visibility,
          same tier as the metadata block above for a non-owner), calling
          the firm-member-gated GET /api/firms/{firmId}/group directly
          (not the platform-admin routes above it). A `null` group is not
          an error - most firms aren't in any group - so this renders
          nothing rather than an error state in that case; only a genuine
          fetch failure shows loadError. */}
      {firmGroupResult.ok && firmGroupResult.group ? (
        <div className="w-full max-w-md rounded-lg border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-950">
          <h2 className="mb-3 text-lg font-semibold text-black dark:text-zinc-50">
            {t("firmGroupTitle")}
          </h2>
          <p className="text-sm text-black dark:text-zinc-50">
            {t("firmGroupBelongsTo", { groupName: firmGroupResult.group.name })}
          </p>
          {firmGroupResult.group.members.length > 0 && (
            <ul className="mt-2 flex flex-col gap-1 text-sm text-zinc-600 dark:text-zinc-400">
              {firmGroupResult.group.members.map((m) => (
                <li key={m.firmId}>
                  {m.firmName} — {t(`firmGroupRole.${m.role}`)}
                </li>
              ))}
            </ul>
          )}
        </div>
      ) : !firmGroupResult.ok ? (
        <p className="w-full max-w-md text-sm text-red-600 dark:text-red-400">
          {t("firmGroupLoadError")}
        </p>
      ) : null}

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

      {role?.isOwner ? (
        <div className="w-full max-w-md rounded-lg border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-950">
          <PortabilityPanel firmId={firm.firmId} />
        </div>
      ) : null}

      {/* Part 4 of the onboarding/help/UX batch: user-scoped, not
          firm-scoped (see internal/identity's own preferences.go doc
          comment) - visible to every firm member regardless of role. */}
      <div className="w-full max-w-md rounded-lg border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-950">
        <h2 className="mb-3 text-lg font-semibold text-black dark:text-zinc-50">
          {t("preferencesTitle")}
        </h2>
        <PreferencesEditor preferences={preferences ?? {}} />
      </div>
    </main>
  );
}
