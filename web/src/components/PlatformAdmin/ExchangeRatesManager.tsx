"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { ExchangeRate } from "@/lib/exchangeRates";

type Props = {
  rates: ExchangeRate[];
};

// The /platform-admin/exchange-rates page's rate list plus a create
// form - manual entry only (this batch's own scope boundary: no
// real-time exchange rate API integration). Platform-admin-gated on the
// backend (internal/currency.handleCreateRate); this form has no
// permission tag of its own to carry since there is no per-firm
// permission-catalog key for a platform-wide, allowlist-gated action -
// same reasoning the platform-admin page itself is exempt from
// Permission Audit Mode's tagging convention.
export default function ExchangeRatesManager({ rates }: Props) {
  const t = useTranslations("ExchangeRates");
  const router = useRouter();

  const [fromCurrency, setFromCurrency] = useState("");
  const [toCurrency, setToCurrency] = useState("");
  const [rate, setRate] = useState("");
  const [source, setSource] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!fromCurrency.trim() || !toCurrency.trim() || !rate.trim()) {
      setError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/platform-admin/exchange-rates`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          fromCurrency: fromCurrency.trim().toUpperCase(),
          toCurrency: toCurrency.trim().toUpperCase(),
          rate: rate.trim(),
          validFrom: new Date().toISOString(),
          source: source || undefined,
        }),
      });
      if (!res.ok) {
        setError(t("createError"));
        return;
      }
      setFromCurrency("");
      setToCurrency("");
      setRate("");
      setSource("");
      router.refresh();
    } catch {
      setError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <form
        onSubmit={submitCreate}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newRateTitle")}</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
          <label className="flex flex-col gap-1 text-sm">
            {t("fromCurrency")}
            <input
              value={fromCurrency}
              onChange={(e) => setFromCurrency(e.target.value)}
              placeholder="USD" // i18n-ignore: ISO 4217 currency code example, not translatable text
              className="rounded-md border border-zinc-300 px-2 py-1.5 uppercase dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("toCurrency")}
            <input
              value={toCurrency}
              onChange={(e) => setToCurrency(e.target.value)}
              placeholder="TRY" // i18n-ignore: ISO 4217 currency code example, not translatable text
              className="rounded-md border border-zinc-300 px-2 py-1.5 uppercase dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("rate")}
            <input
              value={rate}
              onChange={(e) => setRate(e.target.value)}
              placeholder="30.00000000"
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("source")}
            <input
              value={source}
              onChange={(e) => setSource(e.target.value)}
              placeholder="manual" // i18n-ignore: example source-tag value, not user-facing copy
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
        </div>
        {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
        <button
          type="submit"
          data-permission-public="true"
          disabled={submitting}
          className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
        >
          {t("create")}
        </button>
      </form>

      {rates.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("fromCurrency")}</th>
                <th className="py-2 pr-4 font-medium">{t("toCurrency")}</th>
                <th className="py-2 pr-4 font-medium">{t("rate")}</th>
                <th className="py-2 pr-4 font-medium">{t("validFrom")}</th>
                <th className="py-2 font-medium">{t("source")}</th>
              </tr>
            </thead>
            <tbody>
              {rates.map((r) => (
                <tr key={r.id} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
                  <td className="py-2 pr-4">{r.fromCurrency}</td>
                  <td className="py-2 pr-4">{r.toCurrency}</td>
                  <td className="py-2 pr-4">{r.rate}</td>
                  <td className="py-2 pr-4 whitespace-nowrap">{new Date(r.validFrom).toLocaleString()}</td>
                  <td className="py-2">{r.source ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
