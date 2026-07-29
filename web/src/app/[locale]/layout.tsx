import type { Metadata } from "next";
import { cookies } from "next/headers";
import { NextIntlClientProvider, hasLocale } from "next-intl";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { notFound } from "next/navigation";
import { Geist, Geist_Mono } from "next/font/google";
import { routing } from "@/i18n/routing";
import { fetchMe, fetchRoleInFirm } from "@/lib/me";
import AuditModeClient from "@/components/AuditMode/AuditModeClient";
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

// Resolves just enough identity/role context for Permission Audit Mode's
// toggle (see components/AuditMode/AuditModeClient.tsx) to decide what to
// show, without redirecting anyone - unlike page-level fetchMe calls
// (e.g. the stock page), the root layout renders for unauthenticated
// visitors and the wizard's pre-firm screen too, so "no firm yet" here
// just means the toggle doesn't render, not an error.
async function resolveAuditModeContext(): Promise<{
  firmId: string | null;
  isOwner: boolean;
}> {
  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  if (!sessionToken) return { firmId: null, isOwner: false };

  const me = await fetchMe(sessionToken);
  if (!me || me.firms.length === 0) return { firmId: null, isOwner: false };

  // Same first-membership simplifying assumption the stock page uses -
  // no firm-switcher UI exists yet.
  const firmId = me.firms[0].firmId;
  const role = await fetchRoleInFirm(sessionToken, firmId);
  return { firmId, isOwner: role?.isOwner ?? false };
}

export default async function RootLayout({ children, params }: LayoutProps) {
  const { locale } = await params;
  if (!hasLocale(routing.locales, locale)) {
    notFound();
  }
  setRequestLocale(locale);

  const { firmId, isOwner } = await resolveAuditModeContext();

  return (
    <html
      lang={locale}
      className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}
    >
      <body className="min-h-full flex flex-col">
        <NextIntlClientProvider>
          {children}
          <AuditModeClient firmId={firmId} isOwner={isOwner} />
        </NextIntlClientProvider>
      </body>
    </html>
  );
}
