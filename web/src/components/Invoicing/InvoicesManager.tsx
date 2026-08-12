"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { Invoice } from "@/lib/invoicing";
import type { Customer } from "@/lib/crm";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  invoices: Invoice[];
  customers: Customer[];
  isOwner: boolean;
};

// The /invoices page's invoice list - create is owner-gated
// (server-checked by internal/invoicing's own CreateInvoice; isOwner here
// only controls what renders), mirroring
// components/Customers/CustomersManager.tsx's own create-form/row shape.
// The create form here is deliberately minimal - one line per invoice,
// not a full line-item editor - matching this codebase's "functional, not
// polished" convention for a first pass; internal/invoicing's own
// AddInvoiceLine/UpdateInvoiceLine/DeleteInvoiceLine HTTP endpoints exist
// for later line-editing UI, not exercised from here yet. An invoice
// auto-created by stock_to_sale's record_sale transition (the
// workflow-to-invoicing bridge) appears in this same list with no visual
// distinction beyond its invoiceNumber - clicking through to its own
// detail page (which shows sourceWorkflowInstance) is where that
// provenance becomes visible.
export default function InvoicesManager({ firmId, invoices, customers, isOwner }: Props) {
  const t = useTranslations("Invoices");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [customerId, setCustomerId] = useState("");
  const [issuedDate, setIssuedDate] = useState("");
  const [dueDate, setDueDate] = useState("");
  const [description, setDescription] = useState("");
  const [quantity, setQuantity] = useState("1");
  const [unitPrice, setUnitPrice] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [filter, setFilter] = useState("");

  const visibleInvoices = filter.trim()
    ? invoices.filter((inv) => inv.invoiceNumber.toLowerCase().includes(filter.trim().toLowerCase()))
    : invoices;

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    const trimmedDescription = description.trim();
    if (!issuedDate || !trimmedDescription || !quantity || !unitPrice) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/invoicing/invoices/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          customerId: customerId || undefined,
          issuedDate,
          dueDate: dueDate || undefined,
          lines: [{ description: trimmedDescription, quantity, unitPrice }],
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setCustomerId("");
      setIssuedDate("");
      setDueDate("");
      setDescription("");
      setQuantity("1");
      setUnitPrice("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      {isOwner && (
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("invoicesTitle")}
          </h2>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newInvoice")}
          </button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("customer")}
              <select
                value={customerId}
                onChange={(e) => setCustomerId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("customerNone")}</option>
                {customers.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("issuedDate")}
              <input
                type="date"
                value={issuedDate}
                onChange={(e) => setIssuedDate(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("dueDate")}
              <input
                type="date"
                value={dueDate}
                onChange={(e) => setDueDate(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("lineDescription")}
              <input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("lineQuantity")}
              <input
                value={quantity}
                onChange={(e) => setQuantity(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("lineUnitPrice")}
              <input
                value={unitPrice}
                onChange={(e) => setUnitPrice(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
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
      )}

      {invoices.length === 0 ? (
        <EmptyState message={t("empty")} />
      ) : (
        <div className="panel flex flex-col gap-3 p-0">
          <div className="p-4 pb-0">
            <input
              type="text"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder={t("filterPlaceholder")}
              data-permission-public="true"
              className="w-full max-w-xs rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
            />
          </div>
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnNumber")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnStatus")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnTotal")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnIssuedDate")}</th>
                </tr>
              </thead>
              <tbody>
                {visibleInvoices.map((inv) => (
                  <tr
                    key={inv.id}
                    className="border-b border-zinc-200 text-black last:border-0 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-50 dark:hover:bg-zinc-950"
                  >
                    <td className="py-2 pr-4 pl-4 font-mono text-xs">
                      <Link
                        href={`/invoices/${inv.id}`}
                        data-permission-public="true"
                        className="underline-offset-4 hover:underline"
                      >
                        {inv.invoiceNumber}
                      </Link>
                    </td>
                    <td className="py-2 pr-4">{t(`status.${inv.status}`)}</td>
                    <td className="py-2 pr-4 font-mono">
                      {inv.total} {inv.currency}
                    </td>
                    <td className="py-2 pr-4">{inv.issuedDate}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
