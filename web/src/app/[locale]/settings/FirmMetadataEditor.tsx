"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { routing } from "@/i18n/routing";
import type { FirmMetadata } from "@/lib/firm";

type Props = {
  firmId: string;
  metadata: FirmMetadata;
};

// Item 4's firm-name-only editor, extended by Open Points item 36 to
// cover the broader metadata fields (address/taxId/defaultLocale/
// defaultCurrency/logoUrl - internal/firm.Update). One combined form
// rather than a second settings page or a second endpoint - see
// internal/firm's own doc comment for why this stays one PATCH. Owner-
// gated the same way this component's predecessor was: rendered only
// when the caller is an owner (checked by the server component that
// mounts this, settings/page.tsx), tagged data-permission-public rather
// than data-permission-key - PATCH /api/firms/{firmId} is gated by the
// structural is_owner check (internal/permission.IsOwner), not a
// permission-catalog entry.
export default function FirmMetadataEditor({ firmId, metadata }: Props) {
  const t = useTranslations("Settings");
  const router = useRouter();

  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(metadata.name);
  const [address, setAddress] = useState(metadata.address ?? "");
  const [taxId, setTaxId] = useState(metadata.taxId ?? "");
  const [defaultLocale, setDefaultLocale] = useState(metadata.defaultLocale ?? "");
  const [defaultCurrency, setDefaultCurrency] = useState(metadata.defaultCurrency ?? "");
  const [logoUrl, setLogoUrl] = useState(metadata.logoUrl ?? "");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  function resetToCurrent() {
    setName(metadata.name);
    setAddress(metadata.address ?? "");
    setTaxId(metadata.taxId ?? "");
    setDefaultLocale(metadata.defaultLocale ?? "");
    setDefaultCurrency(metadata.defaultCurrency ?? "");
    setLogoUrl(metadata.logoUrl ?? "");
    setError(null);
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);

    if (name.trim() === "") {
      setError(t("firmNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch("/api/firm/update-name", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          firmId,
          name,
          address,
          taxId,
          defaultLocale,
          defaultCurrency,
          logoUrl,
        }),
      });
      if (!res.ok) {
        setError(
          res.status === 403
            ? t("firmNamePermissionDenied")
            : res.status === 400
              ? t("metadataInvalidError")
              : t("firmNameError"),
        );
        return;
      }
      setEditing(false);
      router.refresh();
    } catch {
      setError(t("firmNameError"));
    } finally {
      setSubmitting(false);
    }
  }

  if (!editing) {
    return (
      <div className="flex flex-col gap-3 text-sm">
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
          <dt className="text-zinc-500 dark:text-zinc-400">
            {/* Part 3 of the multi-language UI/localization depth/fiscal
                compliance batch: internal/firm/handlers.go's
                applyFiscalHints resolves vatNumberLabel server-side (e.g.
                "VKN" for a Turkish firm) from the firm's own
                defaultLocale - shown alongside the generic i18n label
                rather than replacing it, since vatNumberLabel is
                server-owned reference data, not a translated UI string of
                its own. */}
            {metadata.vatNumberLabel
              ? t("taxIdLabelWithVatNumber", { genericLabel: t("taxIdLabel"), vatLabel: metadata.vatNumberLabel })
              : t("taxIdLabel")}
          </dt>
          <dd className="text-black dark:text-zinc-50">
            {metadata.taxId || (
              <span className="text-zinc-400 dark:text-zinc-600">{t("notSet")}</span>
            )}
          </dd>
          {/* taxIdWarning (also a server-resolved fiscal hint, never
              blocking - internal/firm's own "warn, don't block" contract)
              is dynamic content from the backend, not a static UI string,
              so it's shown as-is rather than routed through next-intl -
              same treatment this codebase already gives other
              backend-returned error/status text. */}
          {metadata.taxIdWarning && (
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">{metadata.taxIdWarning}</p>
          )}
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
          <dt className="text-zinc-500 dark:text-zinc-400">{t("defaultCurrencyLabel")}</dt>
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
        <button
          type="button"
          data-permission-public="true"
          onClick={() => {
            resetToCurrent();
            setEditing(true);
          }}
          className="self-start text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
        >
          {t("firmNameEditButton")}
        </button>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} data-permission-public="true" className="flex flex-col gap-3 text-sm">
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}

      <label className="flex flex-col gap-1">
        <span className="text-zinc-500 dark:text-zinc-400">{t("firmNameLabel")}</span>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
        />
      </label>

      <label className="flex flex-col gap-1">
        <span className="text-zinc-500 dark:text-zinc-400">{t("addressLabel")}</span>
        <textarea
          value={address}
          onChange={(e) => setAddress(e.target.value)}
          rows={2}
          className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
        />
      </label>

      <label className="flex flex-col gap-1">
        <span className="text-zinc-500 dark:text-zinc-400">
          {metadata.vatNumberLabel
            ? t("taxIdLabelWithVatNumber", { genericLabel: t("taxIdLabel"), vatLabel: metadata.vatNumberLabel })
            : t("taxIdLabel")}
        </span>
        <input
          type="text"
          value={taxId}
          onChange={(e) => setTaxId(e.target.value)}
          className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
        />
        {/* Non-blocking - the field is still valid to submit even with a
            warning showing, per Part 3's own "warn, don't block"
            contract. */}
        {metadata.taxIdWarning && (
          <span className="text-xs text-amber-700 dark:text-amber-400">{metadata.taxIdWarning}</span>
        )}
      </label>

      <label className="flex flex-col gap-1">
        <span className="text-zinc-500 dark:text-zinc-400">{t("defaultLocaleLabel")}</span>
        <select
          value={defaultLocale}
          onChange={(e) => setDefaultLocale(e.target.value)}
          className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
        >
          <option value="">{t("notSet")}</option>
          {routing.locales.map((locale) => (
            <option key={locale} value={locale}>
              {locale}
            </option>
          ))}
        </select>
      </label>

      <label className="flex flex-col gap-1">
        <span className="text-zinc-500 dark:text-zinc-400">{t("defaultCurrencyLabel")}</span>
        <input
          type="text"
          value={defaultCurrency}
          onChange={(e) => setDefaultCurrency(e.target.value.toUpperCase())}
          placeholder={t("defaultCurrencyPlaceholder")}
          className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
        />
      </label>

      <label className="flex flex-col gap-1">
        <span className="text-zinc-500 dark:text-zinc-400">{t("logoUrlLabel")}</span>
        <input
          type="text"
          value={logoUrl}
          onChange={(e) => setLogoUrl(e.target.value)}
          placeholder={t("logoUrlPlaceholder")}
          className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
        />
      </label>

      <div className="flex items-center gap-2">
        <button
          type="submit"
          data-permission-public="true"
          disabled={submitting}
          className="rounded-full bg-foreground px-3 py-1 text-xs font-medium text-background disabled:opacity-50 dark:hover:bg-[#ccc]"
        >
          {t("firmNameSaveButton")}
        </button>
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setEditing(false)}
          className="rounded-full border border-zinc-300 px-3 py-1 text-xs font-medium text-zinc-700 dark:border-zinc-700 dark:text-zinc-300"
        >
          {t("firmNameCancelButton")}
        </button>
      </div>
    </form>
  );
}
