"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Asset, AssetStatus, MaintenanceSchedule, MaintenanceRecord } from "@/lib/asset";
import type { Person } from "@/lib/hr";

type Props = {
  firmId: string;
  asset: Asset;
  schedules: MaintenanceSchedule[];
  records: MaintenanceRecord[];
  people: Person[];
  isOwner: boolean;
};

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

// The /assets/[assetId] detail page: full asset fields, its maintenance
// schedule list (owner-gated create form), and maintenance record history
// (owner-gated log-maintenance form). Mirrors WarehouseDetail.tsx's own
// layout/action conventions. Date fields use <input type="date"> and send
// the raw "YYYY-MM-DD" .value string, never .toISOString() - the
// backend's date columns (purchaseDate, warrantyExpires, lastDoneAt,
// performedAt, nextServiceDate) expect a plain date string.
export default function AssetDetail({ firmId, asset, schedules, records, people, isOwner }: Props) {
  const t = useTranslations("Assets");
  const router = useRouter();

  // Maintenance schedule create form.
  const [creatingSchedule, setCreatingSchedule] = useState(false);
  const [scheduleName, setScheduleName] = useState("");
  const [intervalDays, setIntervalDays] = useState("");
  const [lastDoneAt, setLastDoneAt] = useState("");
  const [scheduleAssignedTo, setScheduleAssignedTo] = useState("");
  const [scheduleError, setScheduleError] = useState<string | null>(null);
  const [submittingSchedule, setSubmittingSchedule] = useState(false);

  async function submitCreateSchedule(e: FormEvent) {
    e.preventDefault();
    setScheduleError(null);
    const days = parseInt(intervalDays, 10);
    if (!scheduleName.trim() || !days || days <= 0) {
      setScheduleError(t("scheduleCreateRequired"));
      return;
    }

    setSubmittingSchedule(true);
    try {
      const res = await fetch(`/api/asset/assets/${firmId}/${asset.id}/maintenance-schedules`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: scheduleName.trim(),
          intervalDays: days,
          lastDoneAt: lastDoneAt || undefined,
          assignedToPersonId: scheduleAssignedTo || undefined,
        }),
      });
      if (!res.ok) {
        setScheduleError(t("scheduleCreateError"));
        return;
      }
      setCreatingSchedule(false);
      setScheduleName("");
      setIntervalDays("");
      setLastDoneAt("");
      setScheduleAssignedTo("");
      router.refresh();
    } catch {
      setScheduleError(t("scheduleCreateError"));
    } finally {
      setSubmittingSchedule(false);
    }
  }

  // Maintenance record ("log maintenance") create form.
  const [loggingRecord, setLoggingRecord] = useState(false);
  const [recordScheduleId, setRecordScheduleId] = useState("");
  const [performedAt, setPerformedAt] = useState("");
  const [performedByPersonId, setPerformedByPersonId] = useState("");
  const [description, setDescription] = useState("");
  const [cost, setCost] = useState("");
  const [nextServiceDate, setNextServiceDate] = useState("");
  const [recordError, setRecordError] = useState<string | null>(null);
  const [submittingRecord, setSubmittingRecord] = useState(false);

  async function submitLogRecord(e: FormEvent) {
    e.preventDefault();
    setRecordError(null);
    if (!performedAt || !description.trim()) {
      setRecordError(t("recordCreateRequired"));
      return;
    }

    setSubmittingRecord(true);
    try {
      const res = await fetch(`/api/asset/assets/${firmId}/${asset.id}/maintenance-records`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          scheduleId: recordScheduleId || undefined,
          performedAt,
          performedByPersonId: performedByPersonId || undefined,
          description: description.trim(),
          cost: cost.trim() || undefined,
          nextServiceDate: nextServiceDate || undefined,
        }),
      });
      if (!res.ok) {
        setRecordError(t("recordCreateError"));
        return;
      }
      setLoggingRecord(false);
      setRecordScheduleId("");
      setPerformedAt("");
      setPerformedByPersonId("");
      setDescription("");
      setCost("");
      setNextServiceDate("");
      router.refresh();
    } catch {
      setRecordError(t("recordCreateError"));
    } finally {
      setSubmittingRecord(false);
    }
  }

  return (
    <div className="flex w-full max-w-2xl flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{asset.name}</h1>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusBadgeClass(asset.status)}`}>
          {t(`statuses.${asset.status}`)}
        </span>
      </div>

      <div className="panel flex flex-col gap-2 p-4 text-sm">
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("assetNumber")}</span>
          <span className="font-mono">{asset.assetNumber}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("type")}</span>
          <span>{t(`types.${asset.type}`)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("assignedTo")}</span>
          <span>{personName(people, asset.assignedToPersonId)}</span>
        </div>
        {asset.purchaseDate && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("purchaseDate")}</span>
            <span>{asset.purchaseDate}</span>
          </div>
        )}
        {asset.purchasePrice && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("purchasePrice")}</span>
            <span className="font-mono">{asset.purchasePrice}</span>
          </div>
        )}
        {asset.currentValue && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("currentValue")}</span>
            <span className="font-mono">{asset.currentValue}</span>
          </div>
        )}
        {asset.depreciationRate && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("depreciationRate")}</span>
            <span className="font-mono">{asset.depreciationRate}</span>
          </div>
        )}
        {asset.serialNumber && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("serialNumber")}</span>
            <span className="font-mono">{asset.serialNumber}</span>
          </div>
        )}
        {asset.warrantyExpires && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("warrantyExpires")}</span>
            <span>{asset.warrantyExpires}</span>
          </div>
        )}
        {asset.notes && (
          <div className="flex flex-col gap-1 border-t border-zinc-200 pt-2 dark:border-zinc-800">
            <span className="text-zinc-600 dark:text-zinc-400">{t("notes")}</span>
            <span>{asset.notes}</span>
          </div>
        )}
      </div>

      <div className="panel flex flex-col gap-3 p-4">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("schedulesTitle")}</h2>
          {isOwner && (
            <button
              type="button"
              data-permission-public="true"
              onClick={() => setCreatingSchedule((v) => !v)}
              className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            >
              {creatingSchedule ? t("cancel") : t("newSchedule")}
            </button>
          )}
        </div>

        {creatingSchedule && (
          <form
            onSubmit={submitCreateSchedule}
            data-permission-public="true"
            className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
          >
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <label className="flex flex-col gap-1 text-sm">
                {t("scheduleName")}
                <input
                  value={scheduleName}
                  onChange={(e) => setScheduleName(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("intervalDays")}
                <input
                  type="number"
                  min={1}
                  value={intervalDays}
                  onChange={(e) => setIntervalDays(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("lastDoneAt")}
                <input
                  type="date"
                  value={lastDoneAt}
                  onChange={(e) => setLastDoneAt(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("assignedTo")}
                <select
                  value={scheduleAssignedTo}
                  onChange={(e) => setScheduleAssignedTo(e.target.value)}
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
            </div>
            {scheduleError && <p className="text-sm text-red-600 dark:text-red-400">{scheduleError}</p>}
            <button
              type="submit"
              data-permission-public="true"
              disabled={submittingSchedule}
              className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
            >
              {t("create")}
            </button>
          </form>
        )}

        {schedules.length === 0 ? (
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("schedulesEmpty")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium">{t("scheduleName")}</th>
                  <th className="py-2 pr-4 font-medium">{t("intervalDays")}</th>
                  <th className="py-2 pr-4 font-medium">{t("nextDueAt")}</th>
                  <th className="py-2 pr-4 font-medium">{t("assignedTo")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnStatus")}</th>
                </tr>
              </thead>
              <tbody>
                {schedules.map((s) => (
                  <tr key={s.id} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                    <td className="py-2 pr-4">{s.name}</td>
                    <td className="py-2 pr-4 font-mono">{s.intervalDays}</td>
                    <td className="py-2 pr-4">{s.nextDueAt ?? "—"}</td>
                    <td className="py-2 pr-4">{personName(people, s.assignedToPersonId)}</td>
                    <td className="py-2 pr-4">{s.isActive ? t("scheduleActive") : t("scheduleInactive")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="panel flex flex-col gap-3 p-4">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("recordsTitle")}</h2>
          {isOwner && (
            <button
              type="button"
              data-permission-public="true"
              onClick={() => setLoggingRecord((v) => !v)}
              className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            >
              {loggingRecord ? t("cancel") : t("logMaintenance")}
            </button>
          )}
        </div>

        {loggingRecord && (
          <form
            onSubmit={submitLogRecord}
            data-permission-public="true"
            className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
          >
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <label className="flex flex-col gap-1 text-sm">
                {t("relatedSchedule")}
                <select
                  value={recordScheduleId}
                  onChange={(e) => setRecordScheduleId(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                >
                  <option value="">{t("noSchedule")}</option>
                  {schedules.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("performedAt")}
                <input
                  type="date"
                  value={performedAt}
                  onChange={(e) => setPerformedAt(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("performedBy")}
                <select
                  value={performedByPersonId}
                  onChange={(e) => setPerformedByPersonId(e.target.value)}
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
                {t("cost")}
                <input
                  value={cost}
                  onChange={(e) => setCost(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("nextServiceDate")}
                <input
                  type="date"
                  value={nextServiceDate}
                  onChange={(e) => setNextServiceDate(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm sm:col-span-2">
                {t("description")}
                <input
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
            </div>
            {recordError && <p className="text-sm text-red-600 dark:text-red-400">{recordError}</p>}
            <button
              type="submit"
              data-permission-public="true"
              disabled={submittingRecord}
              className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
            >
              {t("create")}
            </button>
          </form>
        )}

        {records.length === 0 ? (
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("recordsEmpty")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium">{t("performedAt")}</th>
                  <th className="py-2 pr-4 font-medium">{t("description")}</th>
                  <th className="py-2 pr-4 font-medium">{t("performedBy")}</th>
                  <th className="py-2 pr-4 font-medium">{t("cost")}</th>
                </tr>
              </thead>
              <tbody>
                {records.map((r) => (
                  <tr key={r.id} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                    <td className="py-2 pr-4">{r.performedAt}</td>
                    <td className="py-2 pr-4">{r.description}</td>
                    <td className="py-2 pr-4">{personName(people, r.performedByPersonId)}</td>
                    <td className="py-2 pr-4 font-mono">{r.cost ?? "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
