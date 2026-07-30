// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchMe, fetchRoleInFirm } from "@/lib/me";
import { resolveActiveFirm } from "@/lib/activeFirm";
import { fetchAuditLog } from "@/lib/auditlog";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Item 4 - the missing other half of PR 8: a UI for
// internal/auditlog.List (GET /api/firms/{firmID}/audit-log), which
// existed and was tested since PR 8 but never had a page. Gated to the
// owner role only, for now - Vision §3's "Auditor Role" that would also
// get read access isn't designed or built yet (no invitation flow, see
// docs/OPEN_POINTS.md item 17), so this page only checks isOwner, the
// same simplifying gate every other owner-only surface in this codebase
// uses (e.g. components/AuditMode/AuditModeClient.tsx). The backend
// itself is gated by internal/auditlog.ReadPermission, not a hardcoded
// owner check - this page's gate is a UI-level mirror of that, not the
// enforcement boundary itself.
export default async function AuditLogPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("AuditLog");

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  const me = sessionToken ? await fetchMe(sessionToken) : null;

  if (!me) {
    redirect(`/${locale}`);
  }
  if (me.firms.length === 0) {
    redirect(`/${locale}/wizard`);
  }

  const firm = await resolveActiveFirm(me);
  const role = await fetchRoleInFirm(sessionToken!, firm.firmId);

  if (!role?.isOwner) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-4 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
        <h1 className="text-2xl font-semibold text-black dark:text-zinc-50">
          {t("title")}
        </h1>
        <p className="text-red-600 dark:text-red-400">{t("notAuthorized")}</p>
      </main>
    );
  }

  const entries = await fetchAuditLog(sessionToken!, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-6 bg-zinc-50 px-6 py-16 dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("title")}
      </h1>

      {entries === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : entries.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="w-full max-w-4xl overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("columnWhen")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnWho")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnEntityType")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnEntityId")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnAction")}</th>
                <th className="py-2 font-medium">{t("columnChanges")}</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => (
                <tr
                  key={entry.id}
                  className="border-b border-zinc-200 align-top text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4 whitespace-nowrap">
                    {new Date(entry.occurredAt).toLocaleString()}
                  </td>
                  <td className="py-2 pr-4">
                    {entry.userDisplayName || entry.userEmail}
                  </td>
                  {/* entityType/action are audit_log.entity_type/action
                      values from the backend - data, not UI copy, same
                      convention StockList.tsx documents for workflow
                      state/action names. */}
                  <td className="py-2 pr-4">{entry.entityType}</td>
                  <td className="py-2 pr-4 font-mono text-xs">
                    {entry.entityId}
                  </td>
                  <td className="py-2 pr-4">{entry.action}</td>
                  <td className="py-2 font-mono text-xs whitespace-pre-wrap">
                    {JSON.stringify(entry.changes)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </main>
  );
}
