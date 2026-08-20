// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { notFound } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchPlatformAdminFirms } from "@/lib/platformAdmin";
import PlatformAdminNav from "@/components/PlatformAdmin/PlatformAdminNav";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Read-only view of internal/platformadmin's one endpoint: firm
// name/created_at, workflow-definition count, member count - platform
// metadata only, never tenant business data (see that package's doc
// comment). Reachable only at this distinct path outside any firm
// namespace (not /settings/..., not firm-scoped) - there is no link to
// this whole area anywhere in the app's own nav (Nav/NavShell.tsx); it's
// meant for the small, hardcoded set of ZonaryOS staff on
// internal/platformadmin.Allowlist to navigate to directly.
// PlatformAdminNav below only cross-links the platform-admin pages to
// each other once a staff member has already landed on one of them - see
// that component's own doc comment.
//
// Gating deliberately diverges from settings/permissions/page.tsx's and
// audit-log/page.tsx's pattern (an inline "not authorized" message, which
// still reveals the feature exists to anyone who can reach the route).
// This page's own task brief explicitly calls for the stronger posture:
// anyone not on the allowlist gets next/navigation's notFound(), the real
// Next.js 404 page, exactly as if this route were never defined. That's a
// deliberate, higher-sensitivity choice for this one route - not a sign
// the other owner-gated pages should be changed to match; those stay as
// they are.
//
// fetchPlatformAdminFirms returns null both when the backend rejects the
// caller (its own allowlist check, independently enforced -
// internal/platformadmin/handlers.go) and on any other failure (missing
// session, backend unreachable) - this page treats every one of those
// identically, with no branch that could leak "you're logged in but not
// allowed" versus "something went wrong" versus "you're not logged in".
export default async function PlatformAdminPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("PlatformAdmin");

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  if (!sessionToken) {
    notFound();
  }

  const firms = await fetchPlatformAdminFirms(sessionToken);
  if (firms === null) {
    notFound();
  }

  return (
    <main className="flex flex-1 flex-col items-center gap-6 bg-[var(--color-platform-accent)] px-6 py-16">
      {/* Deliberately NOT the app's zinc/black neutral palette - a deep
          indigo accent used nowhere else in ZonaryOS, so this section is
          visually unmistakable as platform-staff-only tooling and could
          never be confused with a firm-owner settings page in a
          screenshot (see this page's own doc comment above on why the
          gating here is stricter than everywhere else too). */}
      <div className="flex w-full max-w-4xl flex-col items-center gap-3">
        <span className="rounded-full bg-[var(--color-platform-accent-strong)] px-3 py-1 text-xs font-semibold tracking-wide text-[var(--color-platform-on-accent)] uppercase">
          {t("badge")}
        </span>
        <h1 className="text-3xl font-semibold tracking-tight text-[var(--color-platform-on-accent)]">
          {t("title")}
        </h1>
        <p className="max-w-2xl text-center text-sm text-indigo-200">
          {t("description")}
        </p>
      </div>

      <PlatformAdminNav locale={locale} active="home" />

      {firms.length === 0 ? (
        <p className="text-indigo-200">{t("empty")}</p>
      ) : (
        <div className="w-full max-w-4xl overflow-x-auto rounded-lg border border-indigo-800 bg-black/20">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-indigo-800 text-indigo-200">
                <th className="py-2 pr-4 pl-4 font-medium">{t("columnName")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnCreatedAt")}</th>
                <th className="py-2 pr-4 font-medium">
                  {t("columnWorkflowDefinitionCount")}
                </th>
                <th className="py-2 pr-4 font-medium">{t("columnMemberCount")}</th>
              </tr>
            </thead>
            <tbody>
              {firms.map((firm) => (
                <tr
                  key={firm.firmId}
                  className="border-b border-indigo-900 align-top text-[var(--color-platform-on-accent)] last:border-0"
                >
                  <td className="py-2 pr-4 pl-4">{firm.name}</td>
                  <td className="py-2 pr-4 whitespace-nowrap">
                    {new Date(firm.createdAt).toLocaleString()}
                  </td>
                  <td className="py-2 pr-4">{firm.workflowDefinitionCount}</td>
                  <td className="py-2 pr-4">{firm.memberCount}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </main>
  );
}
