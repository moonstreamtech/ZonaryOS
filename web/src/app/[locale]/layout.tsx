// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { Metadata, Viewport } from "next";
import { cookies } from "next/headers";
import { NextIntlClientProvider, hasLocale } from "next-intl";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { notFound } from "next/navigation";
import { Geist, Geist_Mono } from "next/font/google";
import { routing } from "@/i18n/routing";
import { fetchMe, fetchRoleInFirm, type MeResponse } from "@/lib/me";
import { resolveActiveFirm } from "@/lib/activeFirm";
import NavShell from "@/components/Nav/NavShell";
import TelemetryClient from "@/components/Telemetry/TelemetryClient";
import PageViewTracker from "@/components/Analytics/PageViewTracker";
import ServiceWorkerRegistration from "@/components/ServiceWorkerRegistration";
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

// Multi-language UI/localization depth/fiscal compliance batch, Part 2:
// locales that read right-to-left. Only "ar" today - see
// docs/DEVELOPMENT.md's "Multi-language UI..." section for why the RTL
// foundation (dir="rtl" + logical CSS properties) ships ahead of an
// actual professional Arabic translation.
const RTL_LOCALES = new Set(["ar"]);

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
    // PWA foundation batch, Part 2: manifest.json/icon.svg live in
    // public/ (see docs/DEVELOPMENT.md's PWA section). theme_color here
    // mirrors manifest.json's own theme_color/background_color, both set
    // to the sidebar's dark zinc-900 (#18181b - see NavShell.tsx's own
    // doc comment confirming that hex, and styles/tokens.css's
    // --color-sidebar-bg).
    manifest: "/manifest.json",
    icons: [{ rel: "icon", url: "/icon.svg", type: "image/svg+xml" }],
  };
}

// theme_color lives in Next's separate Viewport export (not Metadata) as
// of this Next version - same #18181b as manifest.json's theme_color/
// background_color, see generateMetadata's own comment above.
export const viewport: Viewport = {
  themeColor: "#18181b",
};

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
      dir={RTL_LOCALES.has(locale) ? "rtl" : "ltr"}
      className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}
    >
      <body className="min-h-full flex flex-col bg-zinc-50 dark:bg-black">
        <NextIntlClientProvider>
          {/* Renders nothing - see that component's own doc comment for
              its default-off guarantee (NEXT_PUBLIC_ZONARYOS_TELEMETRY_ENABLED). */}
          <TelemetryClient />
          {/* Renders nothing - fires a page_view analytics event
              (internal/analytics) on every route change once a firm is
              resolved; see that component's own doc comment. */}
          <PageViewTracker firmId={firmId} />
          {/* PWA foundation batch, Part 2: registers public/sw.js -
              renders nothing, see that component's own doc comment. */}
          <ServiceWorkerRegistration />
          <div className="flex min-h-full flex-1">
            <NavShell me={me} activeFirmId={firmId} isOwner={isOwner} />
            <div className="flex min-h-full flex-1 flex-col">
              {/* Mobile-nav rework (this batch): the old slim top bar
                  (GlobalSearchBox + NotificationBell above the page
                  content) is gone - both now live in NavShell's sidebar
                  bottom section instead, so mobile page content starts at
                  the very top with nothing above it. `pb-16` reserves
                  room below `lg` for MobileBottomNav's fixed bar (also
                  mounted from NavShell) so it doesn't cover the last bit
                  of content; `lg:pb-0` drops that padding once the bottom
                  bar itself is hidden. */}
              <div className="flex flex-1 flex-col pb-16 lg:pb-0">{children}</div>
            </div>
          </div>
        </NextIntlClientProvider>
      </body>
    </html>
  );
}
