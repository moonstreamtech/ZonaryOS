// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations, setRequestLocale } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchFirmMembers, fetchFirmPermissionAudit } from "@/lib/permissionAudit";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchInvites } from "@/lib/invite";
import InvitesManager from "@/components/AuditMode/InvitesManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Item 3: a view-only firm roster - who's in the firm, and which role
// they hold (internal/permission.ListMembers, GET .../members).
// Membership-gated only (requireFirmContext, same as /stock and
// /workflows), not owner-only - the backend doesn't treat "who's on my
// team" as owner-only sensitive information, so this page doesn't either.
//
// The invites panel below it is owner-gated (fetchRoleInFirm's isOwner,
// same tier as /settings/permissions) - it's this batch's resolution of
// the "no way to get a second real user into a firm" gap the roster's own
// old copy used to call out explicitly. Open Points item 17 (email
// integration) stays fully deferred either way: nothing here sends
// anything anywhere, an owner copies a link out of InvitesManager and
// shares it by whatever channel they already use - see
// docs/DEVELOPMENT.md's Invites section.
export default async function MembersPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Members");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const [members, role] = await Promise.all([
    fetchFirmMembers(sessionToken, firm.firmId),
    fetchRoleInFirm(sessionToken, firm.firmId),
  ]);

  const isOwner = role?.isOwner ?? false;
  const [invites, audit] = isOwner
    ? await Promise.all([
        fetchInvites(sessionToken, firm.firmId),
        fetchFirmPermissionAudit(sessionToken, firm.firmId),
      ])
    : [null, null];

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
          {members.length === 1 && !isOwner && (
            <p className="mt-4 text-center text-xs text-zinc-500 dark:text-zinc-500">
              {t("onlyMemberNote")}
            </p>
          )}
        </div>
      )}

      {isOwner && (
        <>
          {invites === null || audit === null ? (
            <p className="text-red-600 dark:text-red-400">{t("invitesLoadError")}</p>
          ) : (
            <InvitesManager
              firmId={firm.firmId}
              locale={locale}
              invites={invites}
              roles={audit.roles}
            />
          )}
        </>
      )}
    </main>
  );
}
