// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchMe } from "@/lib/me";
import { fetchDefinitionByKey, fetchInstances } from "@/lib/workflow";
import StockList from "./StockList";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// The well-known workflow key seeded for every firm by the firm-creation
// wizard (internal/wizard.CreateDefaultFirm -> workflow.SeedStockToSaleWorkflowTx)
// - see internal/workflow/stock_to_sale.go's StockToSaleKey constant.
const STOCK_TO_SALE_KEY = "stock_to_sale";

// Server component: resolves the caller's identity, firm, and the Stock
// In -> Sale workflow's current instances, all server-side through the
// existing cookie-to-Bearer proxy pattern (see lib/me.ts, lib/wizard.ts).
// Interactivity (the "sell" action) lives in the client component below.
export default async function StockPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Stock");

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  const me = sessionToken ? await fetchMe(sessionToken) : null;

  if (!me) {
    redirect(`/${locale}`);
  }
  if (me.firms.length === 0) {
    redirect(`/${locale}/wizard`);
  }

  // This vertical slice has no firm-switcher UI yet (Vision §3's
  // many-firms-per-user model is real, but choosing among several active
  // memberships is a separate, not-yet-built screen) - the first
  // membership is used, same simplifying assumption the homepage's firm
  // list implicitly carries.
  const firmId = me.firms[0].firmId;

  const definition = await fetchDefinitionByKey(
    sessionToken!,
    firmId,
    STOCK_TO_SALE_KEY,
  );
  if (!definition) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-4 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
        <h1 className="text-2xl font-semibold text-black dark:text-zinc-50">
          {t("title")}
        </h1>
        <p className="text-red-600 dark:text-red-400">
          {t("definitionMissing")}
        </p>
      </main>
    );
  }

  const instances = (await fetchInstances(
    sessionToken!,
    firmId,
    definition.definitionId,
  )) ?? [];

  return (
    <StockList
      firmId={firmId}
      instances={instances}
    />
  );
}
