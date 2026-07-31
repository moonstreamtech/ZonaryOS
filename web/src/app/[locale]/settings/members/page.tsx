// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations, setRequestLocale } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchFirmMembers } from "@/lib/permissionAudit";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Item 3: a view-only firm roster - who's in the firm, and which role
// they hold (internal/permission.ListMembers, GET .../members).
// Membership-gated only (requireFirmContext, same as /stock and
// /workflows), not owner-only - the backend doesn't treat "who's on my
// team" as owner-only sensitive information, so this page doesn't either.
// With no invite/email flow yet (docs/OPEN_POINTS.md item 17, still
// deferred), this will only ever show the one row for whoever ran the
// firm-creation wizard today - expected, forward-compatible groundwork,
// not a claim that onboarding a second user already works. The empty/
// single-member state below is written to look intentional, not like a
// broken or half-loaded page.
export default async function MembersPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Members");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const members = await fetchFirmMembers(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-6 bg-zinc-50 px-6 py-16 dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("title")}
      </h1>
      <p className="max-w-2xl text-center text-sm text-zinc-600 dark:text-zinc-400">
        {t("description")}
      </p>

      {members === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : members.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="w-full max-w-2xl overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("columnName")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnEmail")}</th>
                <th className="py-2 font-medium">{t("columnRole")}</th>
              </tr>
            </thead>
            <tbody>
              {members.map((member) => (
                <tr
                  key={`${member.userId}:${member.roleId}`}
                  className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4">{member.displayName}</td>
                  <td className="py-2 pr-4">{member.email}</td>
                  {/* member.roleName is roles.name from the backend -
                      data, not UI copy, same convention as workflow
                      state/action names elsewhere in this codebase. */}
                  <td className="py-2">{member.roleName}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {members.length === 1 && (
            <p className="mt-4 text-center text-xs text-zinc-500 dark:text-zinc-500">
              {t("onlyMemberNote")}
            </p>
          )}
        </div>
      )}
    </main>
  );
}
