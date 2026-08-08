// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchDeliveries, type DeliveryStatus } from "@/lib/logistics";
import DeliveriesManager from "@/components/Logistics/DeliveriesManager";

type PageProps = {
  params: Promise<{ locale: string }>;
  searchParams: Promise<{ status?: string }>;
};

const STATUS_OPTIONS: DeliveryStatus[] = ["pending", "in_transit", "delivered", "returned", "cancelled"];

// Logistics management batch's delivery board: deliveries created either
// directly (owner-gated) or automatically by stock_to_sale's record_sale
// transition (the workflow-to-logistics bridge, internal/workflow's
// TransitionSpec.Delivery - see internal/logistics's own package doc
// comment). Member-gated read, owner-gated write, same tier as
// /inventory - this page renders for any member, but DeliveriesManager
// only shows create/status-change controls when isOwner.
export default async function LogisticsPage({ params, searchParams }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Logistics");
  const { status: statusFilter } = await searchParams;

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const deliveries = await fetchDeliveries(sessionToken, firm.firmId, {
    status: STATUS_OPTIONS.includes(statusFilter as DeliveryStatus) ? (statusFilter as DeliveryStatus) : undefined,
  });

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
      </div>

      <form method="get" className="flex w-full max-w-4xl flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("filterStatus")}
          <select
            name="status"
            defaultValue={statusFilter ?? ""}
            className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          >
            <option value="">{t("filterAll")}</option>
            {STATUS_OPTIONS.map((s) => (
              <option key={s} value={s}>
                {t(`status.${s}`)}
              </option>
            ))}
          </select>
        </label>
        <button
          type="submit"
          data-permission-public="true"
          className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white dark:bg-zinc-50 dark:text-black"
        >
          {t("applyButton")}
        </button>
      </form>

      {deliveries === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <DeliveriesManager firmId={firm.firmId} deliveries={deliveries} isOwner={isOwner} />
      )}
    </main>
  );
}
