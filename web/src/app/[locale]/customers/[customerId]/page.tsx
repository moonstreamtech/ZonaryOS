// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { Link } from "@/i18n/navigation";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchCustomer, fetchTimeline, fetchOpportunities } from "@/lib/crm";
import { fetchInstancesByCustomer } from "@/lib/workflow";
import { fetchRoleInFirm } from "@/lib/me";
import CustomerTimeline from "@/components/Crm/CustomerTimeline";

type PageProps = {
  params: Promise<{ locale: string; customerId: string }>;
};

// Part 3c of the multi-tenant isolation hardening + performance batch:
// "the first real CRM view that connects sales to customers" - the
// customer detail page the design brief calls for. Shows the lead/CRM
// pipeline instance that created this customer
// (customer.sourceWorkflowInstance, already exposed by internal/crm's
// own GetCustomer response) and every sale (any workflow instance whose
// payload.customer_id references this customer -
// internal/workflow.ListInstancesByCustomer) that references it. Both
// link through to the minimal workflow-instance detail page Part 3a
// added. Member-gated, same tier as /customers itself.
export default async function CustomerDetailPage({ params }: PageProps) {
  const { locale, customerId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("CustomerDetail");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [customer, sales, timeline, opportunities] = await Promise.all([
    fetchCustomer(sessionToken, firm.firmId, customerId),
    fetchInstancesByCustomer(sessionToken, firm.firmId, customerId),
    fetchTimeline(sessionToken, firm.firmId, customerId),
    fetchOpportunities(sessionToken, firm.firmId, { customerId }),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {customer === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <div className="flex w-full max-w-3xl flex-col gap-8">
          <div className="flex flex-col gap-2 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
            <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{customer.name}</h1>
            <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
              <div>
                <dt className="text-zinc-500 dark:text-zinc-400">{t("email")}</dt>
                <dd className="text-black dark:text-zinc-50">{customer.email ?? "—"}</dd>
              </div>
              <div>
                <dt className="text-zinc-500 dark:text-zinc-400">{t("phone")}</dt>
                <dd className="text-black dark:text-zinc-50">{customer.phone ?? "—"}</dd>
              </div>
              {customer.sourceWorkflowInstance && (
                <div>
                  <dt className="text-zinc-500 dark:text-zinc-400">{t("sourceLead")}</dt>
                  <dd>
                    <Link
                      href={`/workflows/instance/${customer.sourceWorkflowInstance}`}
                      data-permission-public="true"
                      className="text-black underline-offset-4 hover:underline dark:text-zinc-50"
                    >
                      {t("sourceLeadLink")}
                    </Link>
                  </dd>
                </div>
              )}
            </dl>
          </div>

          <div className="flex flex-col gap-3">
            <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("salesTitle")}</h2>
            {sales === null ? (
              <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
            ) : sales.length === 0 ? (
              <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("noSales")}</p>
            ) : (
              <ul className="flex flex-col gap-2">
                {sales.map((s) => (
                  <li
                    key={s.id}
                    className="flex items-center justify-between rounded-md border border-zinc-300 p-3 text-sm dark:border-zinc-700"
                  >
                    <Link
                      href={`/workflows/instance/${s.id}`}
                      data-permission-public="true"
                      className="font-medium text-black underline-offset-4 hover:underline dark:text-zinc-50"
                    >
                      {s.definitionName}
                    </Link>
                    <span className="text-xs text-zinc-500 dark:text-zinc-400">{s.stateName}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <CustomerTimeline
            firmId={firm.firmId}
            customerId={customerId}
            timeline={timeline}
            opportunities={opportunities ?? []}
            isOwner={isOwner}
          />
        </div>
      )}
    </main>
  );
}
