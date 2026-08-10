"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { Delivery, DeliveryStatus } from "@/lib/logistics";
import type { InstanceInvoice } from "@/lib/workflow";

type Props = {
  firmId: string;
  deliveries: Delivery[];
  isOwner: boolean;
  // Part 3b: for a delivery whose source is a workflow instance, the
  // first invoice that instance's own workflow-to-invoicing bridge
  // created (if any) - keyed by delivery id, fetched server-side (see
  // app/[locale]/logistics/page.tsx).
  sourceInvoiceByDeliveryId: Record<string, InstanceInvoice | undefined>;
};

const STATUS_OPTIONS: DeliveryStatus[] = ["pending", "in_transit", "delivered", "returned", "cancelled"];

// The /logistics page's delivery board: deliveries created either
// directly (owner-gated, via the form below) or automatically by
// stock_to_sale's record_sale transition (the workflow-to-logistics
// bridge) - create/status-change controls are owner-gated (server-checked
// by internal/logistics's own CreateDelivery/UpdateDelivery; isOwner here
// only controls what renders), mirroring
// components/Inventory/InventoryManager.tsx's own create-form/row-action
// shape.
export default function DeliveriesManager({ firmId, deliveries, isOwner, sourceInvoiceByDeliveryId }: Props) {
  const t = useTranslations("Logistics");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [reference, setReference] = useState("");
  const [destinationAddress, setDestinationAddress] = useState("");
  const [originAddress, setOriginAddress] = useState("");
  const [carrier, setCarrier] = useState("");
  const [trackingNumber, setTrackingNumber] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    const trimmedDestination = destinationAddress.trim();
    if (!trimmedDestination) {
      setCreateError(t("createDestinationRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/logistics/deliveries/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          reference: reference || undefined,
          destinationAddress: trimmedDestination,
          originAddress: originAddress || undefined,
          carrier: carrier || undefined,
          trackingNumber: trackingNumber || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setReference("");
      setDestinationAddress("");
      setOriginAddress("");
      setCarrier("");
      setTrackingNumber("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function updateStatus(delivery: Delivery, status: DeliveryStatus) {
    try {
      await fetch(`/api/logistics/deliveries/${firmId}/${delivery.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      });
      router.refresh();
    } catch {
      // best-effort - the row simply won't reflect the change; a retry
      // is always available via the same select.
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      {isOwner && (
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("deliveriesTitle")}
          </h2>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newDelivery")}
          </button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("reference")}
              <input
                value={reference}
                onChange={(e) => setReference(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("destinationAddress")}
              <input
                value={destinationAddress}
                onChange={(e) => setDestinationAddress(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("originAddress")}
              <input
                value={originAddress}
                onChange={(e) => setOriginAddress(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("carrier")}
              <input
                value={carrier}
                onChange={(e) => setCarrier(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("trackingNumber")}
              <input
                value={trackingNumber}
                onChange={(e) => setTrackingNumber(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
          </div>
          {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
          <button
            type="submit"
            data-permission-public="true"
            disabled={submitting}
            className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
          >
            {t("create")}
          </button>
        </form>
      )}

      {deliveries.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("reference")}</th>
                <th className="py-2 pr-4 font-medium">{t("destinationAddress")}</th>
                <th className="py-2 pr-4 font-medium">{t("carrier")}</th>
                <th className="py-2 pr-4 font-medium">{t("trackingNumber")}</th>
                <th className="py-2 pr-4 font-medium">{t("status.label")}</th>
                <th className="py-2 font-medium">{t("sourceInvoice")}</th>
              </tr>
            </thead>
            <tbody>
              {deliveries.map((d) => (
                <tr key={d.id} className="border-b border-zinc-200 align-top text-black dark:border-zinc-800 dark:text-zinc-50">
                  <td className="py-2 pr-4 font-mono text-xs">{d.reference ?? "—"}</td>
                  <td className="py-2 pr-4">{d.destinationAddress ?? "—"}</td>
                  <td className="py-2 pr-4">{d.carrier ?? "—"}</td>
                  <td className="py-2 pr-4 font-mono text-xs">{d.trackingNumber ?? "—"}</td>
                  <td className="py-2 pr-4">
                    {isOwner ? (
                      <select
                        value={d.status}
                        data-permission-public="true"
                        onChange={(e) => updateStatus(d, e.target.value as DeliveryStatus)}
                        className="rounded-md border border-zinc-300 px-2 py-1 text-xs dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                      >
                        {STATUS_OPTIONS.map((s) => (
                          <option key={s} value={s}>
                            {t(`status.${s}`)}
                          </option>
                        ))}
                      </select>
                    ) : (
                      t(`status.${d.status}`)
                    )}
                  </td>
                  <td className="py-2">
                    {d.sourceType === "workflow_instance" && d.sourceId ? (
                      sourceInvoiceByDeliveryId[d.id] ? (
                        <Link
                          href={`/invoices/${sourceInvoiceByDeliveryId[d.id]!.id}`}
                          data-permission-public="true"
                          className="text-xs text-black underline-offset-4 hover:underline dark:text-zinc-50"
                        >
                          {sourceInvoiceByDeliveryId[d.id]!.invoiceNumber}
                        </Link>
                      ) : (
                        <Link
                          href={`/workflows/instance/${d.sourceId}`}
                          data-permission-public="true"
                          className="text-xs text-zinc-600 underline-offset-4 hover:underline dark:text-zinc-400"
                        >
                          {t("sourceInstanceLink")}
                        </Link>
                      )
                    ) : (
                      <span className="text-xs text-zinc-400 dark:text-zinc-600">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
