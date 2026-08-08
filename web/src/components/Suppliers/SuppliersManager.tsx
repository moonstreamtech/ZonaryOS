"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Supplier } from "@/lib/inventory";

type Props = {
  firmId: string;
  suppliers: Supplier[];
  isOwner: boolean;
};

// The /suppliers page's supplier list - create/edit are owner-gated
// (server-checked by internal/inventory's own CreateSupplier/UpdateSupplier;
// isOwner here only controls what renders), mirroring
// components/HR/PeopleManager.tsx's own create-form/row-action shape.
export default function SuppliersManager({ firmId, suppliers, isOwner }: Props) {
  const t = useTranslations("Suppliers");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [contactName, setContactName] = useState("");
  const [email, setEmail] = useState("");
  const [phone, setPhone] = useState("");
  const [currency, setCurrency] = useState("TRY");
  const [paymentTerms, setPaymentTerms] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    const trimmedName = name.trim();
    if (!trimmedName) {
      setCreateError(t("createNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/inventory/suppliers/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: trimmedName,
          contactName: contactName || undefined,
          email: email || undefined,
          phone: phone || undefined,
          currency: currency || undefined,
          paymentTerms: paymentTerms || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setName("");
      setContactName("");
      setEmail("");
      setPhone("");
      setCurrency("TRY");
      setPaymentTerms("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function toggleActive(supplier: Supplier) {
    try {
      await fetch(`/api/inventory/suppliers/${firmId}/${supplier.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ isActive: !supplier.isActive }),
      });
      router.refresh();
    } catch {
      // best-effort - the row simply won't reflect the change; a retry
      // is always available via the same button.
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      {isOwner && (
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("suppliersTitle")}
          </h2>
          <button
            type="button"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newSupplier")}
          </button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={submitCreate}
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
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
              {t("contactName")}
              <input
                value={contactName}
                onChange={(e) => setContactName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("email")}
              <input
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("phone")}
              <input
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("currency")}
              <input
                value={currency}
                onChange={(e) => setCurrency(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("paymentTerms")}
              <input
                value={paymentTerms}
                onChange={(e) => setPaymentTerms(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
          </div>
          {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
          <button
            type="submit"
            disabled={submitting}
            className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
          >
            {t("create")}
          </button>
        </form>
      )}

      {suppliers.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {suppliers.map((supplier) => (
            <div
              key={supplier.id}
              className="flex items-center justify-between gap-2 rounded-md border border-zinc-300 p-3 text-sm dark:border-zinc-700"
            >
              <div className="flex flex-col">
                <span className="font-medium text-black dark:text-zinc-50">{supplier.name}</span>
                <span className="text-xs text-zinc-500 dark:text-zinc-400">
                  {supplier.contactName ?? "—"} · {supplier.currency}
                </span>
              </div>
              <div className="flex items-center gap-2">
                <span className="text-xs text-zinc-500 dark:text-zinc-400">
                  {supplier.isActive ? t("statusActive") : t("statusInactive")}
                </span>
                {isOwner && (
                  <button
                    type="button"
                    onClick={() => toggleActive(supplier)}
                    className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                  >
                    {supplier.isActive ? t("deactivate") : t("activate")}
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
