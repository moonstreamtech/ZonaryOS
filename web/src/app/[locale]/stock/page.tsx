// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchMe } from "@/lib/me";
import { resolveActiveFirm } from "@/lib/activeFirm";
import {
  fetchDefinitionByKey,
  fetchInstances,
  STOCK_TO_SALE_KEY,
} from "@/lib/workflow";
import { fetchAuditLog } from "@/lib/auditlog";
import StockList from "./StockList";
import AddStockForm from "./AddStockForm";
import StockHistory from "./StockHistory";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Server component: resolves the caller's identity, active firm (see
// lib/activeFirm.ts - no longer a hardcoded me.firms[0], see item 3), and
// the Stock In -> Sale workflow's current instances, all server-side
// through the existing cookie-to-Bearer proxy pattern (see lib/me.ts,
// lib/wizard.ts). Interactivity (add stock, sell) lives in the client
// components below.
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

  const firm = await resolveActiveFirm(me);
  const firmId = firm.firmId;

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

  // Sale/transaction history (item 6) reuses the audit log's own data
  // rather than a parallel history mechanism - see StockHistory.tsx's
  // doc comment. Not every member can read it: internal/auditlog.List is
  // gated by audit.log.read, held by the owner role by default (see
  // internal/auditlog/auditlog.go) - a null result here just means the
  // section is omitted below, not an error.
  const auditEntries = await fetchAuditLog(sessionToken!, firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-2xl flex-col items-center gap-6">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>

        <AddStockForm
          firmId={firmId}
          definitionId={definition.definitionId}
          createPermissionKey={definition.createPermissionKey}
        />

        <StockList firmId={firmId} instances={instances} />
      </div>

      {auditEntries && (
        <StockHistory instances={instances} entries={auditEntries} />
      )}
    </main>
  );
}
