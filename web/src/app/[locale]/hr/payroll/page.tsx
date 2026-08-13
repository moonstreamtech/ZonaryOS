// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchPeople } from "@/lib/hr";
import { fetchPeriods } from "@/lib/payroll";
import PayrollManager from "@/components/Payroll/PayrollManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Payroll foundation batch: payroll period list, with entries and a
// close action shown per period (owner-only, permission-tagged) - see
// internal/payroll's own package doc comment. Member-gated read,
// owner-gated write, same tier as /hr's own People page.
export default async function PayrollPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Payroll");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const periods = await fetchPeriods(sessionToken, firm.firmId);
  const people = (await fetchPeople(sessionToken, firm.firmId)) ?? [];

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
      </div>

      {periods === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <PayrollManager firmId={firm.firmId} periods={periods} people={people} isOwner={isOwner} />
      )}
    </main>
  );
}
