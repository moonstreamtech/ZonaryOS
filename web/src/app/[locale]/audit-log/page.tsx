// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchMe, fetchRoleInFirm } from "@/lib/me";
import { resolveActiveFirm } from "@/lib/activeFirm";
import { fetchAuditLogPage } from "@/lib/auditlog";
import { fetchDefinitions } from "@/lib/workflow";
import AuditLogTable from "./AuditLogTable";

type PageProps = {
  params: Promise<{ locale: string }>;
  searchParams: Promise<{ page?: string; from?: string; to?: string; definitionId?: string }>;
};

// Page size for the audit log below - not user-configurable (no UI for
// it), just the fixed page size pagination pages through, same
// convention as components/Workflow/WorkflowDefinitionView.tsx's
// INSTANCES_PAGE_SIZE.
const AUDIT_LOG_PAGE_SIZE = 20;

// Item 4's original page (the missing other half of PR 8: a UI for
// internal/auditlog.List, GET /api/firms/{firmID}/audit-log), extended to
// add pagination and date-range/workflow-definition filtering
// (internal/auditlog.ListOptions) - the page rendered every entry
// unpaginated until this batch. Gated to the owner role only, for now -
// Vision §3's "Auditor Role" that would also get read access isn't
// designed or built yet (no invitation flow, see docs/OPEN_POINTS.md item
// 17), so this page only checks isOwner, the same simplifying gate every
// other owner-only surface in this codebase uses (e.g.
// components/AuditMode/AuditModeClient.tsx). The backend itself is gated
// by internal/auditlog.ReadPermission, not a hardcoded owner check - this
// page's gate is a UI-level mirror of that, not the enforcement boundary
// itself.
export default async function AuditLogPage({ params, searchParams }: PageProps) {
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

  const { page: pageParam, from, to, definitionId } = await searchParams;
  const page = Math.max(1, pageParam ? Number(pageParam) : 1);

  const [result, definitions] = await Promise.all([
    fetchAuditLogPage(sessionToken!, firm.firmId, {
      limit: AUDIT_LOG_PAGE_SIZE,
      offset: (page - 1) * AUDIT_LOG_PAGE_SIZE,
      from,
      to,
      definitionId,
    }),
    fetchDefinitions(sessionToken!, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-6 bg-zinc-50 px-6 py-16 dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("title")}
      </h1>

      {result === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <AuditLogTable
          entries={result.entries}
          total={result.total}
          page={page}
          pageSize={AUDIT_LOG_PAGE_SIZE}
          from={from ?? ""}
          to={to ?? ""}
          definitionId={definitionId ?? ""}
          definitions={definitions ?? []}
        />
      )}
    </main>
  );
}
