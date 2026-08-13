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

  const tAdmin = await getTranslations("PlatformAdmin");

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-[var(--color-platform-accent)] px-6 py-16">
      {/* Same deliberately-not-zinc indigo accent as /platform-admin
          itself - see that page's own doc comment on why this section
          needs a visually unmistakable identity. */}
      <div className="flex w-full max-w-3xl flex-col items-center gap-3">
        <span className="rounded-full bg-[var(--color-platform-accent-strong)] px-3 py-1 text-xs font-semibold tracking-wide text-[var(--color-platform-on-accent)] uppercase">
          {tAdmin("badge")}
        </span>
        <h1 className="text-3xl font-semibold tracking-tight text-[var(--color-platform-on-accent)]">{t("title")}</h1>
        <p className="max-w-2xl text-center text-sm text-indigo-200">{t("description")}</p>
      </div>

      {rates === null ? (
        <p className="text-red-300">{t("loadError")}</p>
      ) : (
        <div className="w-full max-w-3xl rounded-lg border border-indigo-800 bg-white p-4 dark:bg-zinc-950">
          <ExchangeRatesManager rates={rates} />
        </div>
      )}
    </main>
  );
}
