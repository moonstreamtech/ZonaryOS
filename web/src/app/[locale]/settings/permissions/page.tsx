// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchMe, fetchRoleInFirm } from "@/lib/me";
import { resolveActiveFirm } from "@/lib/activeFirm";
import { fetchFirmPermissionAudit, fetchFirmPermissionCatalog } from "@/lib/permissionAudit";
import RoleManager from "@/components/AuditMode/RoleManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Item 3: a dedicated, static permission-management page - the full
// picture Permission Audit Mode's live badge overlay only ever shows one
// element at a time, on whatever screen happens to render it. Owner-
// gated the same way audit-log/page.tsx is (isOwner, not a permission
// key - see docs/DEVELOPMENT.md's Permission Audit Mode section for why
// "authorized" means is_owner in this codebase), and reads from
// internal/permission.GetFirmPermissionCatalog, which itself is built on
// top of GetFirmPermissionAudit - Audit Mode's own grant/revoke popup's
// data source - rather than a second, parallel query path. The
// permission-key table below is still read-only: granting/revoking still
// happens through Audit Mode's live popup (PermissionPopup.tsx), which
// already covers that.
//
// Item 5 extends this same page with role create/rename/delete
// (RoleManager, below) rather than a separate page - a firm's roles and
// its permission grants are the same underlying concept (Vision §3's
// Parametric Permission/Role System), and RoleManager deliberately
// doesn't render each role's own grants itself: the permission-key table
// it sits above already shows, for every key, which role keys hold it -
// a newly created role simply not appearing in any row yet *is* "starts
// with zero grants," reusing that existing render path instead of
// duplicating it.
export default async function PermissionsSettingsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("PermissionSettings");

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

  const [catalog, audit] = await Promise.all([
    fetchFirmPermissionCatalog(sessionToken!, firm.firmId),
    fetchFirmPermissionAudit(sessionToken!, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
        <p className="max-w-2xl text-center text-sm text-zinc-600 dark:text-zinc-400">
          {t("description")}
        </p>
      </div>

      {audit === null ? (
        <p className="text-red-600 dark:text-red-400">{t("rolesLoadError")}</p>
      ) : (
        <RoleManager firmId={firm.firmId} roles={audit.roles} />
      )}

      {catalog === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : catalog.entries.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="w-full max-w-4xl overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("columnKey")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnDescription")}</th>
                <th className="py-2 font-medium">{t("columnRoles")}</th>
              </tr>
            </thead>
            <tbody>
              {catalog.entries.map((entry) => (
                <tr
                  key={entry.key}
                  className="border-b border-zinc-200 align-top text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  {/* entry.key/description are permissions.key/description
                      from the backend's global catalog - data, not UI
                      copy, same convention as workflow state/action
                      names elsewhere in this codebase. */}
                  <td className="py-2 pr-4 font-mono text-xs whitespace-nowrap">
                    {entry.key}
                  </td>
                  <td className="py-2 pr-4">{entry.description}</td>
                  <td className="py-2">
                    {entry.roleKeys.length === 0 ? (
                      <span className="text-zinc-400 dark:text-zinc-600">
                        {t("noRoles")}
                      </span>
                    ) : (
                      <div className="flex flex-wrap gap-1">
                        {entry.roleKeys.map((roleKey) => (
                          <span
                            key={roleKey}
                            className="rounded-full bg-zinc-200 px-2 py-0.5 text-xs text-black dark:bg-zinc-800 dark:text-zinc-50"
                          >
                            {roleKey}
                          </span>
                        ))}
                      </div>
                    )}
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
