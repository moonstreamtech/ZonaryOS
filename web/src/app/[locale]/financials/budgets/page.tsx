// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchBudgets } from "@/lib/budget";
import BudgetsManager from "@/components/Budget/BudgetsManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Budget management module (internal/budget): the budget list, with an
// owner-gated create form - mirrors /contracts/page.tsx's own
// "member-read, owner-write" list page shape. Reading the list is
// ordinary firm data visibility (the same tier as ListJournalEntries on
// /financials itself); only creating a budget, adding lines, and
// approving are owner-only (isOwner here only controls what renders -
// internal/budget's own handlers are the real authorization boundary).
export default async function BudgetsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Budgets");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const budgets = await fetchBudgets(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
        <p className="max-w-2xl text-center text-sm text-zinc-600 dark:text-zinc-400">
          {t("description")}
        </p>
      </div>

      {budgets === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <BudgetsManager firmId={firm.firmId} budgets={budgets} isOwner={isOwner} />
      )}
    </main>
  );
}
