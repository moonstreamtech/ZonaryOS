"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Invoice, InvoiceStatus, Payment } from "@/lib/invoicing";

type Props = {
  firmId: string;
  invoice: Invoice;
  payments: Payment[];
  isOwner: boolean;
};

// A manual status move never includes 'paid' - that status is exclusively
// RecordPayment's (backend-enforced, internal/invoicing.UpdateInvoiceStatus
// rejects a direct move to it) - mirrors that same allowed-transitions
// state machine on the frontend side, so the owner never sees an option
// the backend would reject anyway.
const MANUAL_STATUS_OPTIONS: Record<InvoiceStatus, InvoiceStatus[]> = {
  draft: ["sent", "cancelled"],
  sent: ["overdue", "cancelled"],
  overdue: ["sent", "cancelled"],
  paid: [],
  cancelled: [],
};

// The /invoices/[invoiceId] detail page's body: read-only line list
// (full line-item editing stays a backend-only capability for now, same
// "functional, not polished" scope InvoicesManager's own doc comment
// notes), a manual status-change control (owner-gated, gated further by
// MANUAL_STATUS_OPTIONS above), recorded payments, and the "Record
// Payment" form the design brief calls for - owner-gated, posts to
// internal/invoicing.RecordPayment via the Next proxy route, which may
// auto-close the invoice to 'paid' and post a real journal entry (the
// receivables cycle's closing half).
export default function InvoiceDetail({ firmId, invoice, payments, isOwner }: Props) {
  const t = useTranslations("Invoices");
  const router = useRouter();

  const [statusSubmitting, setStatusSubmitting] = useState(false);
  const [statusError, setStatusError] = useState<string | null>(null);

  const [amount, setAmount] = useState("");
  const [paidAt, setPaidAt] = useState("");
  const [method, setMethod] = useState("");
  const [reference, setReference] = useState("");
  const [paymentError, setPaymentError] = useState<string | null>(null);
  const [paymentSubmitting, setPaymentSubmitting] = useState(false);

  async function changeStatus(status: InvoiceStatus) {
    setStatusError(null);
    setStatusSubmitting(true);
    try {
      const res = await fetch(`/api/invoicing/invoices/${firmId}/${invoice.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      });
      if (!res.ok) {
        setStatusError(t("statusUpdateError"));
        return;
      }
      router.refresh();
    } catch {
      setStatusError(t("statusUpdateError"));
    } finally {
      setStatusSubmitting(false);
    }
  }

  async function submitPayment(e: FormEvent) {
    e.preventDefault();
    setPaymentError(null);
    if (!amount || !paidAt) {
      setPaymentError(t("paymentRequired"));
      return;
    }

    setPaymentSubmitting(true);
    try {
      const res = await fetch(`/api/invoicing/invoices/${firmId}/${invoice.id}/payments`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          amount,
          paidAt: new Date(paidAt).toISOString(),
          method: method || undefined,
          reference: reference || undefined,
        }),
      });
      if (!res.ok) {
        setPaymentError(t("paymentError"));
        return;
      }
      setAmount("");
      setPaidAt("");
      setMethod("");
      setReference("");
      router.refresh();
    } catch {
      setPaymentError(t("paymentError"));
    } finally {
      setPaymentSubmitting(false);
    }
  }

  const manualOptions = MANUAL_STATUS_OPTIONS[invoice.status];

  return (
    <div className="flex w-full max-w-4xl flex-col gap-8">
      <div className="flex flex-col gap-2 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {invoice.invoiceNumber}
          </h2>
          <div className="flex items-center gap-3">
            <span className="text-sm text-zinc-600 dark:text-zinc-400">{t(`status.${invoice.status}`)}</span>
            <a
              href={`/api/documents/invoices/${firmId}/${invoice.id}/render`}
              target="_blank"
              rel="noopener noreferrer"
              data-permission-public="true"
              className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            >
              {t("preview")}
            </a>
          </div>
        </div>
        <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("issuedDate")}</dt>
            <dd className="text-black dark:text-zinc-50">{invoice.issuedDate}</dd>
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("dueDate")}</dt>
            <dd className="text-black dark:text-zinc-50">{invoice.dueDate ?? "—"}</dd>
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("columnTotal")}</dt>
            <dd className="font-mono text-black dark:text-zinc-50">
              {invoice.total} {invoice.currency}
            </dd>
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("subtotalAndTax")}</dt>
            <dd className="font-mono text-black dark:text-zinc-50">
              {invoice.subtotal} + {invoice.taxAmount}
            </dd>
          </div>
        </dl>

        {isOwner && manualOptions.length > 0 && (
          <div className="flex flex-wrap items-center gap-2 pt-2">
            <span className="text-xs text-zinc-500 dark:text-zinc-400">{t("changeStatus")}</span>
            {manualOptions.map((s) => (
              <button
                key={s}
                type="button"
                data-permission-public="true"
                disabled={statusSubmitting}
                onClick={() => changeStatus(s)}
                className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
              >
                {t(`status.${s}`)}
              </button>
            ))}
            {statusError && <span className="text-xs text-red-600 dark:text-red-400">{statusError}</span>}
          </div>
        )}
      </div>

      <div className="flex flex-col gap-3">
        <h3 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("linesTitle")}</h3>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("lineDescription")}</th>
                <th className="py-2 pr-4 font-medium">{t("lineQuantity")}</th>
                <th className="py-2 pr-4 font-medium">{t("lineUnitPrice")}</th>
                <th className="py-2 font-medium">{t("lineTotal")}</th>
              </tr>
            </thead>
            <tbody>
              {(invoice.lines ?? []).map((l) => (
                <tr key={l.id} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
                  <td className="py-2 pr-4">{l.description}</td>
                  <td className="py-2 pr-4 font-mono">{l.quantity}</td>
                  <td className="py-2 pr-4 font-mono">{l.unitPrice}</td>
                  <td className="py-2 font-mono">{l.lineTotal}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div className="flex flex-col gap-3">
        <h3 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("paymentsTitle")}</h3>
        {payments.length === 0 ? (
          <p className="text-zinc-600 dark:text-zinc-400">{t("noPayments")}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {payments.map((p) => (
              <li
                key={p.id}
                className="flex items-center justify-between rounded-md border border-zinc-300 p-3 text-sm dark:border-zinc-700"
              >
                <span className="font-mono text-black dark:text-zinc-50">
                  {p.amount} {p.currency}
                </span>
                <span className="text-xs text-zinc-500 dark:text-zinc-400">
                  {new Date(p.paidAt).toLocaleDateString()} · {p.method ?? "—"}
                </span>
              </li>
            ))}
          </ul>
        )}

        {isOwner && invoice.status !== "paid" && invoice.status !== "cancelled" && (
          <form
            onSubmit={submitPayment}
            data-permission-public="true"
            className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
          >
            <h4 className="text-sm font-semibold text-black dark:text-zinc-50">{t("recordPaymentTitle")}</h4>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <label className="flex flex-col gap-1 text-sm">
                {t("paymentAmount")}
                <input
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("paymentDate")}
                <input
                  type="date"
                  value={paidAt}
                  onChange={(e) => setPaidAt(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("paymentMethod")}
                <input
                  value={method}
                  onChange={(e) => setMethod(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("paymentReference")}
                <input
                  value={reference}
                  onChange={(e) => setReference(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
            </div>
            {paymentError && <p className="text-sm text-red-600 dark:text-red-400">{paymentError}</p>}
            <button
              type="submit"
              data-permission-public="true"
              disabled={paymentSubmitting}
              className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
            >
              {t("recordPayment")}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
