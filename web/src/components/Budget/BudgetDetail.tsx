"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Budget, BudgetLine } from "@/lib/budget";
import type { Account, ReportLine } from "@/lib/accounting";

type Props = {
  firmId: string;
  budget: Budget;
  lines: BudgetLine[];
  accounts: Account[];
  actualsByAccountId: Record<string, ReportLine>;
  isOwner: boolean;
};

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

// The /financials/budgets/[budgetId] detail page's client half: an
// owner-gated "add line" form and an owner-only "Approve" action.
// Mirrors ContractDetail.tsx's own layout/action conventions. The
// actual/variance/variance% columns only render once
// actualsByAccountId has an entry for a line's accountId - the server
// component above only populates that map at all when the budget is
// 'active' (see that page's own doc comment on why).
export default function BudgetDetail({
  firmId,
  budget,
  lines,
  accounts,
  actualsByAccountId,
  isOwner,
}: Props) {
  const t = useTranslations("Budgets");
  const router = useRouter();

  const accountsById = Object.fromEntries(accounts.map((a) => [a.id, a]));
  const usedAccountIds = new Set(lines.map((l) => l.accountId));
  const availableAccounts = accounts.filter((a) => !usedAccountIds.has(a.id));

  const [addingLine, setAddingLine] = useState(false);
  const [accountId, setAccountId] = useState("");
  const [plannedAmount, setPlannedAmount] = useState("");
  const [lineNotes, setLineNotes] = useState("");
  const [lineError, setLineError] = useState<string | null>(null);
  const [submittingLine, setSubmittingLine] = useState(false);

  const [approving, setApproving] = useState(false);
  const [approveError, setApproveError] = useState<string | null>(null);

  const hasActuals = budget.status === "active";

  async function submitAddLine(e: FormEvent) {
    e.preventDefault();
    setLineError(null);
    if (!accountId || !plannedAmount.trim()) {
      setLineError(t("lineCreateRequired"));
      return;
    }

    setSubmittingLine(true);
    try {
      const res = await fetch(`/api/budgets/${firmId}/${budget.id}/lines`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          accountId,
          plannedAmount: plannedAmount.trim(),
          notes: lineNotes.trim() || undefined,
        }),
      });
      if (!res.ok) {
        setLineError(t("lineCreateError"));
        return;
      }
      setAddingLine(false);
      setAccountId("");
      setPlannedAmount("");
      setLineNotes("");
      router.refresh();
    } catch {
      setLineError(t("lineCreateError"));
    } finally {
      setSubmittingLine(false);
    }
  }

  async function submitApprove() {
    setApproveError(null);
    setApproving(true);
    try {
      const res = await fetch(`/api/budgets/${firmId}/${budget.id}/approve`, {
        method: "POST",
      });
      if (!res.ok) {
        setApproveError(t("approveError"));
        return;
      }
      router.refresh();
    } catch {
      setApproveError(t("approveError"));
    } finally {
      setApproving(false);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{budget.name}</h1>
        <div className="flex items-center gap-2">
          <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusBadgeClass(budget.status)}`}>
            {t(`statuses.${budget.status}`)}
          </span>
          {isOwner && budget.status === "draft" && (
            <button
              type="button"
              data-permission-public="true"
              disabled={approving}
              onClick={submitApprove}
              className="rounded-md bg-black px-3 py-1.5 text-xs font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
            >
              {t("approve")}
            </button>
          )}
        </div>
      </div>
      {approveError && <p className="text-sm text-red-600 dark:text-red-400">{approveError}</p>}

      <div className="panel flex flex-col gap-2 p-4 text-sm">
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("periodType")}</span>
          <span>{t(`periodTypes.${budget.periodType}`)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("periodStart")}</span>
          <span>{budget.periodStart}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("periodEnd")}</span>
          <span>{budget.periodEnd}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("currency")}</span>
          <span className="font-mono">{budget.currency}</span>
        </div>
        {budget.approvedAt && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("approvedAt")}</span>
            <span>{new Date(budget.approvedAt).toLocaleString()}</span>
          </div>
        )}
        {budget.notes && (
          <div className="flex flex-col gap-1 border-t border-zinc-200 pt-2 dark:border-zinc-800">
            <span className="text-zinc-600 dark:text-zinc-400">{t("notes")}</span>
            <span>{budget.notes}</span>
          </div>
        )}
      </div>

      <div className="panel flex flex-col gap-3 p-4">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("linesTitle")}</h2>
          {isOwner && (
            <button
              type="button"
              data-permission-public="true"
              onClick={() => setAddingLine((v) => !v)}
              className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            >
              {addingLine ? t("cancel") : t("addLine")}
            </button>
          )}
        </div>

        {addingLine && (
          <form
            onSubmit={submitAddLine}
            data-permission-public="true"
            className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
          >
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <label className="flex flex-col gap-1 text-sm">
                {t("lineAccount")}
                <select
                  value={accountId}
                  onChange={(e) => setAccountId(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                >
                  <option value="">{t("lineAccountSelect")}</option>
                  {availableAccounts.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.code} — {a.name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("linePlannedAmount")}
                <input
                  value={plannedAmount}
                  onChange={(e) => setPlannedAmount(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm sm:col-span-2">
                {t("lineNotes")}
                <input
                  value={lineNotes}
                  onChange={(e) => setLineNotes(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
            </div>
            {lineError && <p className="text-sm text-red-600 dark:text-red-400">{lineError}</p>}
            <button
              type="submit"
              data-permission-public="true"
              disabled={submittingLine}
              className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
            >
              {t("create")}
            </button>
          </form>
        )}

        {lines.length === 0 ? (
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("linesEmpty")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium">{t("lineAccount")}</th>
                  <th className="py-2 pr-4 font-medium">{t("linePlannedAmount")}</th>
                  {hasActuals && (
                    <>
                      <th className="py-2 pr-4 font-medium">{t("lineActualAmount")}</th>
                      <th className="py-2 pr-4 font-medium">{t("lineVariance")}</th>
                      <th className="py-2 font-medium">{t("lineVariancePercent")}</th>
                    </>
                  )}
                </tr>
              </thead>
              <tbody>
                {lines.map((l) => {
                  const account = accountsById[l.accountId];
                  const actual = actualsByAccountId[l.accountId];
                  return (
                    <tr key={l.id} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                      <td className="py-2 pr-4">
                        {account ? `${account.code} — ${account.name}` : l.accountId}
                      </td>
                      <td className="py-2 pr-4 font-mono">
                        {l.plannedAmount} {budget.currency}
                      </td>
                      {hasActuals && (
                        <>
                          <td className="py-2 pr-4 font-mono">
                            {actual ? `${actual.amount} ${budget.currency}` : t("lineNoActuals")}
                          </td>
                          <td className="py-2 pr-4 font-mono">
                            {actual?.variance ? `${actual.variance} ${budget.currency}` : "—"}
                          </td>
                          <td className="py-2 font-mono">
                            {actual?.variancePercent ? `${actual.variancePercent}%` : "—"}
                          </td>
                        </>
                      )}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
