// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { Metadata } from "next";
import { cookies } from "next/headers";
import { NextIntlClientProvider, hasLocale } from "next-intl";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { notFound } from "next/navigation";
import { Geist, Geist_Mono } from "next/font/google";
import { routing } from "@/i18n/routing";
import { fetchMe, fetchRoleInFirm, type MeResponse } from "@/lib/me";
import { resolveActiveFirm } from "@/lib/activeFirm";
import NavShell from "@/components/Nav/NavShell";
import GlobalSearchBox from "@/components/Nav/GlobalSearchBox";
import NotificationBell from "@/components/Nav/NotificationBell";
import TelemetryClient from "@/components/Telemetry/TelemetryClient";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export function generateStaticParams() {
  return routing.locales.map((locale) => ({ locale }));
}

type LayoutProps = {
  children: React.ReactNode;
  params: Promise<{ locale: string }>;
};

export async function generateMetadata({
  params,
}: LayoutProps): Promise<Metadata> {
  const { locale } = await params;
  const t = await getTranslations({ locale, namespace: "Home" });
  return {
    title: t("title"),
    description: t("subtitle"),
  };
}

// Resolves just enough identity/role/firm context for the nav shell (see
// components/Nav/NavShell.tsx, which also carries Permission Audit
// Mode's toggle) to decide what to show, without redirecting anyone -
// unlike page-level fetchMe calls (e.g. the stock page), the root layout
// renders for unauthenticated visitors and the wizard's pre-firm screen
// too, so "no firm yet" here just means the firm-scoped nav items and
// Audit Mode toggle don't render, not an error.
async function resolveNavContext(): Promise<{
  me: MeResponse | null;
  firmId: string | null;
  isOwner: boolean;
}> {
  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  if (!sessionToken) return { me: null, firmId: null, isOwner: false };

  const me = await fetchMe(sessionToken);
  if (!me || me.firms.length === 0) return { me, firmId: null, isOwner: false };

  const activeFirm = await resolveActiveFirm(me);
  const role = await fetchRoleInFirm(sessionToken, activeFirm.firmId);
  return { me, firmId: activeFirm.firmId, isOwner: role?.isOwner ?? false };
}

export default async function RootLayout({ children, params }: LayoutProps) {
  const { locale } = await params;
  if (!hasLocale(routing.locales, locale)) {
    notFound();
  }
  setRequestLocale(locale);

  const { me, firmId, isOwner } = await resolveNavContext();

  return (
    <html
      lang={locale}
      className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}
    >
      <body className="min-h-full flex flex-col bg-zinc-50 dark:bg-black">
        <NextIntlClientProvider>
          {/* Renders nothing - see that component's own doc comment for
              its default-off guarantee (NEXT_PUBLIC_ZONARYOS_TELEMETRY_ENABLED). */}
          <TelemetryClient />
          <div className="flex min-h-full flex-1">
            <NavShell me={me} activeFirmId={firmId} isOwner={isOwner} />
            <div className="flex min-h-full flex-1 flex-col">
              {/* Sidebar restructure (this batch) moved GlobalSearchBox
                  and NotificationBell out of the old top header into
                  this slim bar, which now runs above the page content
                  instead of beside the nav - see NavShell.tsx's doc
                  comment. Only rendered once there's an active firm,
                  same gate the old header used for both. */}
              {firmId && (
                <div className="flex items-center justify-end gap-3 border-b border-zinc-200 bg-white px-4 py-2 dark:border-zinc-800 dark:bg-black">
                  <GlobalSearchBox />
                  <NotificationBell firmId={firmId} />
                </div>
              )}
              <div className="flex flex-1 flex-col">{children}</div>
            </div>
          </div>
        </NextIntlClientProvider>
      </body>
    </html>
  );
}
