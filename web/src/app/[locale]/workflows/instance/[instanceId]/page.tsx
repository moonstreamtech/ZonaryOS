// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { Link } from "@/i18n/navigation";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchInstanceState, fetchInstanceInvoices } from "@/lib/workflow";
import { formatPayload } from "@/components/Workflow/format";

type PageProps = {
  params: Promise<{ locale: string; instanceId: string }>;
};

// A minimal workflow-instance detail page (Part 3a of the multi-tenant
// isolation hardening + performance batch) - the destination for an
// invoice detail page's own new "Source" link. State/payload read via
// the same GET .../workflow-instances/{instanceId} endpoint the
// transitions flow already uses (internal/workflow.CurrentState);
// "Associated invoices" is this batch's new cross-link
// (ListInvoicesBySourceInstance). Member-gated, same tier as
// /workflows/[key].
export default async function WorkflowInstanceDetailPage({ params }: PageProps) {
  const { locale, instanceId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("WorkflowInstanceDetail");

  const { sessionToken, firm } = await requireFirmContext(locale);

  const [state, invoices] = await Promise.all([
    fetchInstanceState(sessionToken, firm.firmId, instanceId),
    fetchInstanceInvoices(sessionToken, firm.firmId, instanceId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {state === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <div className="flex w-full max-w-3xl flex-col gap-8">
          <div className="flex flex-col gap-2 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
            <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
            <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
              <div>
                <dt className="text-zinc-500 dark:text-zinc-400">{t("state")}</dt>
                <dd className="text-black dark:text-zinc-50">{state.state.name}</dd>
              </div>
              <div>
                <dt className="text-zinc-500 dark:text-zinc-400">{t("payload")}</dt>
                <dd className="text-black dark:text-zinc-50">{formatPayload(state.payload) || "—"}</dd>
              </div>
            </dl>
          </div>

          <div className="flex flex-col gap-3">
            <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">
              {t("associatedInvoicesTitle")}
            </h2>
            {invoices === null ? (
              <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
            ) : invoices.length === 0 ? (
              <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("noInvoices")}</p>
            ) : (
              <ul className="flex flex-col gap-2">
                {invoices.map((inv) => (
                  <li
                    key={inv.id}
                    className="flex items-center justify-between rounded-md border border-zinc-300 p-3 text-sm dark:border-zinc-700"
                  >
                    <Link
                      href={`/invoices/${inv.id}`}
                      data-permission-public="true"
                      className="font-medium text-black underline-offset-4 hover:underline dark:text-zinc-50"
                    >
                      {inv.invoiceNumber}
                    </Link>
                    <span className="font-mono text-xs text-zinc-500 dark:text-zinc-400">
                      {inv.total} {inv.currency}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}
    </main>
  );
}
