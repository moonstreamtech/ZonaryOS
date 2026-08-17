"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { Budget, BudgetPeriodType } from "@/lib/budget";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  budgets: Budget[];
  isOwner: boolean;
};

const PERIOD_TYPES: BudgetPeriodType[] = ["monthly", "quarterly", "annual"];

function statusBadgeClass(status: Budget["status"]): string {
  switch (status) {
    case "active":
      return "bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-400";
    case "approved":
      return "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-400";
    case "draft":
      return "bg-zinc-200 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300";
    case "closed":
      return "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-400";
  }
}

// The /financials/budgets page's budget list - mirrors
// ContractsManager.tsx's own create-form/row/status-badge shape. Create
// is owner-gated (server-checked by internal/budget.CreateBudget;
// isOwner here only controls what renders). Date fields use
// <input type="date"> and send the raw "YYYY-MM-DD" .value string,
// never .toISOString() - the backend's periodStart/periodEnd are DATE
// columns parsed via internal/budget.parseDate, not timestamps. Only
// one budget can be 'active' per overlapping period - if this create
// form is used to create one directly as 'active', the backend flips
// any other overlapping active budget back to 'approved' automatically
// (see lib/budget.ts's own doc comment on createBudget); this UI does
// not attempt to predict or prevent that, only report the create
// succeeded.
export default function BudgetsManager({ firmId, budgets, isOwner }: Props) {
  const t = useTranslations("Budgets");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [periodType, setPeriodType] = useState<BudgetPeriodType>("monthly");
  const [periodStart, setPeriodStart] = useState("");
  const [periodEnd, setPeriodEnd] = useState("");
  const [currency, setCurrency] = useState("");
  const [notes, setNotes] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim() || !periodStart || !periodEnd) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/budgets/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: name.trim(),
          periodType,
          periodStart,
          periodEnd,
          currency: currency.trim() || undefined,
          notes: notes.trim() || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setName("");
      setPeriodType("monthly");
      setPeriodStart("");
      setPeriodEnd("");
      setCurrency("");
      setNotes("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      <div className="flex items-center justify-end">
        {isOwner && (
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newBudget")}
          </button>
        )}
      </div>

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
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
              {t("periodType")}
              <select
                value={periodType}
                onChange={(e) => setPeriodType(e.target.value as BudgetPeriodType)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {PERIOD_TYPES.map((pt) => (
                  <option key={pt} value={pt}>
                    {t(`periodTypes.${pt}`)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("periodStart")}
              <input
                type="date"
                value={periodStart}
                onChange={(e) => setPeriodStart(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("periodEnd")}
              <input
                type="date"
                value={periodEnd}
                onChange={(e) => setPeriodEnd(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("currency")}
              <input
                value={currency}
                onChange={(e) => setCurrency(e.target.value)}
                placeholder="TRY" // i18n-ignore: an ISO 4217 currency code example, not translatable copy
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("notes")}
              <input
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
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

      {budgets.length === 0 ? (
        <EmptyState message={t("budgetsEmpty")} />
      ) : (
        <div className="panel flex flex-col gap-3 p-0">
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnName")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnPeriod")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnStatus")}</th>
                  <th className="py-2 font-medium">{t("columnCurrency")}</th>
                </tr>
              </thead>
              <tbody>
                {budgets.map((b) => (
                  <tr
                    key={b.id}
                    className="border-b border-zinc-200 text-black last:border-0 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-50 dark:hover:bg-zinc-950"
                  >
                    <td className="py-2 pr-4 pl-4">
                      <Link
                        href={`/financials/budgets/${b.id}`}
                        data-permission-public="true"
                        className="underline-offset-4 hover:underline"
                      >
                        {b.name}
                      </Link>
                    </td>
                    <td className="py-2 pr-4">
                      {t(`periodTypes.${b.periodType}`)} — {b.periodStart} → {b.periodEnd}
                    </td>
                    <td className="py-2 pr-4">
                      <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusBadgeClass(b.status)}`}>
                        {t(`statuses.${b.status}`)}
                      </span>
                    </td>
                    <td className="py-2 font-mono">{b.currency}</td>
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
