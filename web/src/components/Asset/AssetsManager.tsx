"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { Asset, AssetType, AssetStatus } from "@/lib/asset";
import type { Person } from "@/lib/hr";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  assets: Asset[];
  people: Person[];
  isOwner: boolean;
};

const ASSET_TYPES: AssetType[] = ["equipment", "vehicle", "property", "tool", "other"];
const ASSET_STATUSES: AssetStatus[] = ["active", "maintenance", "retired", "disposed"];

function personName(people: Person[], id?: string): string {
  if (!id) return "—";
  return people.find((p) => p.id === id)?.fullName ?? id;
}

function statusBadgeClass(status: AssetStatus): string {
  switch (status) {
    case "active":
      return "bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-400";
    case "maintenance":
      return "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-400";
    case "retired":
      return "bg-zinc-200 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300";
    case "disposed":
      return "bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-400";
  }
}

// The /assets page's asset list - mirrors ProjectsManager.tsx's own
// create-form/row/status-badge shape: create is owner-gated
// (server-checked by internal/asset.CreateAsset; isOwner here only
// controls what renders). Date fields use <input type="date"> and send
// the raw "YYYY-MM-DD" .value string, never .toISOString() - the backend's
// purchaseDate/warrantyExpires are DATE columns, not timestamps.
export default function AssetsManager({ firmId, assets, people, isOwner }: Props) {
  const t = useTranslations("Assets");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [assetNumber, setAssetNumber] = useState("");
  const [type, setType] = useState<AssetType>("equipment");
  const [status, setStatus] = useState<AssetStatus>("active");
  const [assignedToPersonId, setAssignedToPersonId] = useState("");
  const [purchaseDate, setPurchaseDate] = useState("");
  const [purchasePrice, setPurchasePrice] = useState("");
  const [serialNumber, setSerialNumber] = useState("");
  const [warrantyExpires, setWarrantyExpires] = useState("");
  const [notes, setNotes] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim() || !assetNumber.trim()) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/asset/assets/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: name.trim(),
          assetNumber: assetNumber.trim(),
          type,
          status,
          assignedToPersonId: assignedToPersonId || undefined,
          purchaseDate: purchaseDate || undefined,
          purchasePrice: purchasePrice.trim() || undefined,
          serialNumber: serialNumber.trim() || undefined,
          warrantyExpires: warrantyExpires || undefined,
          notes: notes.trim() || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setName("");
      setAssetNumber("");
      setType("equipment");
      setStatus("active");
      setAssignedToPersonId("");
      setPurchaseDate("");
      setPurchasePrice("");
      setSerialNumber("");
      setWarrantyExpires("");
      setNotes("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      {isOwner && (
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("listTitle")}</h2>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newAsset")}
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
              {t("assetName")}
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("assetNumber")}
              <input
                value={assetNumber}
                onChange={(e) => setAssetNumber(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("type")}
              <select
                value={type}
                onChange={(e) => setType(e.target.value as AssetType)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {ASSET_TYPES.map((at) => (
                  <option key={at} value={at}>
                    {t(`types.${at}`)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("status")}
              <select
                value={status}
                onChange={(e) => setStatus(e.target.value as AssetStatus)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {ASSET_STATUSES.map((s) => (
                  <option key={s} value={s}>
                    {t(`statuses.${s}`)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("assignedTo")}
              <select
                value={assignedToPersonId}
                onChange={(e) => setAssignedToPersonId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("selectPerson")}</option>
                {people.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.fullName}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("purchaseDate")}
              <input
                type="date"
                value={purchaseDate}
                onChange={(e) => setPurchaseDate(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("purchasePrice")}
              <input
                value={purchasePrice}
                onChange={(e) => setPurchasePrice(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("serialNumber")}
              <input
                value={serialNumber}
                onChange={(e) => setSerialNumber(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("warrantyExpires")}
              <input
                type="date"
                value={warrantyExpires}
                onChange={(e) => setWarrantyExpires(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("notes")}
              <input
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
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

      {assets.length === 0 ? (
        <EmptyState message={t("assetsEmpty")} />
      ) : (
        <div className="panel flex flex-col gap-3 p-0">
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnName")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnAssetNumber")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnType")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnStatus")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnAssignedTo")}</th>
                </tr>
              </thead>
              <tbody>
                {assets.map((a) => (
                  <tr
                    key={a.id}
                    className="border-b border-zinc-200 text-black last:border-0 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-50 dark:hover:bg-zinc-950"
                  >
                    <td className="py-2 pr-4 pl-4">
                      <Link
                        href={`/assets/${a.id}`}
                        data-permission-public="true"
                        className="underline-offset-4 hover:underline"
                      >
                        {a.name}
                      </Link>
                    </td>
                    <td className="py-2 pr-4 font-mono text-xs">{a.assetNumber}</td>
                    <td className="py-2 pr-4">{t(`types.${a.type}`)}</td>
                    <td className="py-2 pr-4">
                      <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusBadgeClass(a.status)}`}>
                        {t(`statuses.${a.status}`)}
                      </span>
                    </td>
                    <td className="py-2 pr-4">{personName(people, a.assignedToPersonId)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
