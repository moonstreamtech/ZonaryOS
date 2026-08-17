// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchBudget, fetchBudgetLines } from "@/lib/budget";
import { fetchAccounts, fetchPnLReport, type ReportLine } from "@/lib/accounting";
import BudgetDetail from "@/components/Budget/BudgetDetail";

type PageProps = {
  params: Promise<{ locale: string; budgetId: string }>;
};

// The /financials/budgets/[budgetId] detail page: full budget fields,
// its lines (account, planned amount, and - once the budget is 'active'
// - actual/variance/variance% pulled from the P&L report for the
// budget's own period), an owner-gated "add line" form, and an
// owner-only "Approve" action (drafts -> approved; a separate,
// owner-gated PATCH to status='active' is what internal/budget calls
// "activating"). Member-gated read, owner-gated write - same tier
// /contracts/[contractId] uses.
//
// Actual/variance is only fetched when the budget is 'active', matching
// internal/accounting's attachBudgetVarianceTx (it only attaches
// variance data when an 'active' budget covers the requested period) -
// fetching it for a draft/approved/closed budget would just come back
// empty, so this page skips the extra round trip.
export default async function BudgetDetailPage({ params }: PageProps) {
  const { locale, budgetId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Budgets");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [budget, lines, accounts] = await Promise.all([
    fetchBudget(sessionToken, firm.firmId, budgetId),
    fetchBudgetLines(sessionToken, firm.firmId, budgetId),
    fetchAccounts(sessionToken, firm.firmId),
  ]);

  let actualsByAccountId: Record<string, ReportLine> = {};
  if (budget && budget.status === "active") {
    const report = await fetchPnLReport(sessionToken, firm.firmId, {
      from: `${budget.periodStart}T00:00:00Z`,
      to: `${budget.periodEnd}T23:59:59Z`,
    });
    if (report) {
      actualsByAccountId = Object.fromEntries(
        [...report.revenue, ...report.expenses].map((line) => [line.accountId, line]),
      );
    }
  }

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {budget === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <BudgetDetail
          firmId={firm.firmId}
          budget={budget}
          lines={lines ?? []}
          accounts={accounts ?? []}
          actualsByAccountId={actualsByAccountId}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
