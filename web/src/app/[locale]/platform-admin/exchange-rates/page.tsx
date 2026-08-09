// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { notFound } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchPlatformAdminFirms } from "@/lib/platformAdmin";
import { fetchExchangeRates } from "@/lib/exchangeRates";
import ExchangeRatesManager from "@/components/PlatformAdmin/ExchangeRatesManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Multi-currency foundation's platform-admin management page (this
// batch, internal/currency) - exchange rates are platform-wide, not
// firm-scoped, so this lives under /platform-admin, not /settings.
// Gating mirrors platform-admin/page.tsx's own stronger posture
// (notFound(), not an inline "not authorized" message): re-uses
// fetchPlatformAdminFirms purely as the allowlist probe (the same
// internal/platformadmin.Allowlist gates both endpoints), since a
// failed/non-2xx response there already means "this caller shouldn't
// see this feature exist at all" regardless of which platform-admin
// page they landed on.
export default async function ExchangeRatesPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("ExchangeRates");

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  if (!sessionToken) {
    notFound();
  }

  const allowlistProbe = await fetchPlatformAdminFirms(sessionToken);
  if (allowlistProbe === null) {
    notFound();
  }

  const rates = await fetchExchangeRates();

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-3xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
        <p className="max-w-2xl text-center text-sm text-zinc-600 dark:text-zinc-400">{t("description")}</p>
      </div>

      {rates === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <ExchangeRatesManager rates={rates} />
      )}
    </main>
  );
}
