"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { TaxRate } from "@/lib/localization";

type Props = {
  firmId: string;
  taxRates: TaxRate[];
};

// The /settings/tax-rates page's client half: a create form and a
// per-row delete action, mirroring components/Settings/
// AddressesManager.tsx's own shape. Owner-gated on the backend
// (internal/localization.CreateTaxRate/DeleteTaxRate). Rate is entered
// as a fraction (e.g. "0.2000" for 20%) to match internal/localization.
// TaxRate.Rate's own storage format - the invoice-line override
// (internal/invoicing.resolveLineTaxRate) is the one place that converts
// this to a percentage, not this form.
export default function TaxRatesManager({ firmId, taxRates }: Props) {
  const t = useTranslations("TaxRatesSettings");
  const router = useRouter();

  const [name, setName] = useState("");
  const [rate, setRate] = useState("");
  const [countryCode, setCountryCode] = useState("");
  const [region, setRegion] = useState("");
  const [appliesTo, setAppliesTo] = useState("");
  const [isDefault, setIsDefault] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [pendingTaxRateId, setPendingTaxRateId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ taxRateId: string; message: string } | null>(null);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim() || !rate.trim()) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/localization/tax-rates/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: name.trim(),
          rate: rate.trim(),
          countryCode: countryCode.trim().toUpperCase() || undefined,
          region: region.trim() || undefined,
          appliesTo: appliesTo.trim() || undefined,
          isDefault,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setName("");
      setRate("");
      setCountryCode("");
      setRegion("");
      setAppliesTo("");
      setIsDefault(false);
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDelete(taxRateId: string) {
    setRowError(null);
    setPendingTaxRateId(taxRateId);
    try {
      const res = await fetch(`/api/localization/tax-rates/${firmId}/${taxRateId}`, { method: "DELETE" });
      if (!res.ok) {
        setRowError({ taxRateId, message: t("deleteError") });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ taxRateId, message: t("deleteError") });
    } finally {
      setPendingTaxRateId(null);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <form
        onSubmit={submitCreate}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newTaxRateTitle")}</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="flex flex-col gap-1 text-sm">
            {t("name")}
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("rate")}
            <input
              value={rate}
              onChange={(e) => setRate(e.target.value)}
              placeholder="0.2000"
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("countryCode")}
            <input
              value={countryCode}
              onChange={(e) => setCountryCode(e.target.value)}
              placeholder="US" // i18n-ignore: ISO 3166-1 country code example, not translatable text
              className="rounded-md border border-zinc-300 px-2 py-1.5 uppercase dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("region")}
            <input
              value={region}
              onChange={(e) => setRegion(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("appliesTo")}
            <input
              value={appliesTo}
              onChange={(e) => setAppliesTo(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={isDefault} onChange={(e) => setIsDefault(e.target.checked)} />
            {t("isDefault")}
          </label>
        </div>
        {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
        <button
          type="submit"
          data-permission-public="true"
          disabled={submitting}
          className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
        >
          {t("create")}
        </button>
      </form>

      {taxRates.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("name")}</th>
                <th className="py-2 pr-4 font-medium">{t("rate")}</th>
                <th className="py-2 pr-4 font-medium">{t("countryCode")}</th>
                <th className="py-2 pr-4 font-medium">{t("isDefault")}</th>
                <th className="py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {taxRates.map((r) => (
                <tr key={r.id} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
                  <td className="py-2 pr-4">{r.name}</td>
                  <td className="py-2 pr-4">{r.rate}</td>
                  <td className="py-2 pr-4">{r.countryCode ?? "—"}</td>
                  <td className="py-2 pr-4">{r.isDefault ? t("yes") : t("no")}</td>
                  <td className="py-2 text-right">
                    <button
                      type="button"
                      data-permission-public="true"
                      disabled={pendingTaxRateId === r.id}
                      onClick={() => handleDelete(r.id)}
                      className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
                    >
                      {t("delete")}
                    </button>
                    {rowError?.taxRateId === r.id && (
                      <p className="mt-1 text-xs text-red-600 dark:text-red-400">{rowError.message}</p>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
