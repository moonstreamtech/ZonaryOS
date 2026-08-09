"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Address } from "@/lib/localization";

type Props = {
  firmId: string;
  addresses: Address[];
};

// The /settings/addresses page's client half: a create form and a
// per-row delete action, mirroring components/Accounting/
// AccountsManager.tsx's own shape for owner-gated "firm-structural CRUD,
// no dedicated admin panel" pages. Owner-gated on the backend
// (internal/localization.CreateAddress/DeleteAddress) - tagged
// data-permission-public since firm-structural settings pages in this
// codebase gate on is_owner, not a permission-catalog key (see
// settings/accounts/page.tsx's own doc comment for that convention).
export default function AddressesManager({ firmId, addresses }: Props) {
  const t = useTranslations("AddressesSettings");
  const router = useRouter();

  const [label, setLabel] = useState("");
  const [line1, setLine1] = useState("");
  const [line2, setLine2] = useState("");
  const [city, setCity] = useState("");
  const [stateProvince, setStateProvince] = useState("");
  const [postalCode, setPostalCode] = useState("");
  const [countryCode, setCountryCode] = useState("");
  const [isDefault, setIsDefault] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [pendingAddressId, setPendingAddressId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ addressId: string; message: string } | null>(null);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!line1.trim() || !city.trim() || !countryCode.trim()) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/localization/addresses/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          label: label.trim() || undefined,
          line1: line1.trim(),
          line2: line2.trim() || undefined,
          city: city.trim(),
          stateProvince: stateProvince.trim() || undefined,
          postalCode: postalCode.trim() || undefined,
          countryCode: countryCode.trim().toUpperCase(),
          isDefault,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setLabel("");
      setLine1("");
      setLine2("");
      setCity("");
      setStateProvince("");
      setPostalCode("");
      setCountryCode("");
      setIsDefault(false);
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDelete(addressId: string) {
    setRowError(null);
    setPendingAddressId(addressId);
    try {
      const res = await fetch(`/api/localization/addresses/${firmId}/${addressId}`, { method: "DELETE" });
      if (!res.ok) {
        setRowError({ addressId, message: t("deleteError") });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ addressId, message: t("deleteError") });
    } finally {
      setPendingAddressId(null);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <form
        onSubmit={submitCreate}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newAddressTitle")}</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="flex flex-col gap-1 text-sm">
            {t("label")}
            <input
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("line1")}
            <input
              value={line1}
              onChange={(e) => setLine1(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("line2")}
            <input
              value={line2}
              onChange={(e) => setLine2(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("city")}
            <input
              value={city}
              onChange={(e) => setCity(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("stateProvince")}
            <input
              value={stateProvince}
              onChange={(e) => setStateProvince(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("postalCode")}
            <input
              value={postalCode}
              onChange={(e) => setPostalCode(e.target.value)}
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

      {addresses.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("label")}</th>
                <th className="py-2 pr-4 font-medium">{t("line1")}</th>
                <th className="py-2 pr-4 font-medium">{t("city")}</th>
                <th className="py-2 pr-4 font-medium">{t("countryCode")}</th>
                <th className="py-2 pr-4 font-medium">{t("isDefault")}</th>
                <th className="py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {addresses.map((a) => (
                <tr key={a.id} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
                  <td className="py-2 pr-4">{a.label ?? "—"}</td>
                  <td className="py-2 pr-4">{a.line1}</td>
                  <td className="py-2 pr-4">{a.city}</td>
                  <td className="py-2 pr-4">{a.countryCode}</td>
                  <td className="py-2 pr-4">{a.isDefault ? t("yes") : t("no")}</td>
                  <td className="py-2 text-right">
                    <button
                      type="button"
                      data-permission-public="true"
                      disabled={pendingAddressId === a.id}
                      onClick={() => handleDelete(a.id)}
                      className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
                    >
                      {t("delete")}
                    </button>
                    {rowError?.addressId === a.id && (
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
