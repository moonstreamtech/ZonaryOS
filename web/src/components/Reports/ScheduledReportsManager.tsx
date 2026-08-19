"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { ScheduledReport, ScheduledReportInput, ScheduleInterval } from "@/lib/scheduledreports";
import type { ReportDefinition } from "@/lib/reports";

type Props = {
  firmId: string;
  scheduledReports: ScheduledReport[];
  definitions: ReportDefinition[];
  locale: string;
};

const INTERVALS: ScheduleInterval[] = ["daily", "weekly", "monthly"];

const INTERVAL_KEYS = {
  daily: "intervalDaily",
  weekly: "intervalWeekly",
  monthly: "intervalMonthly",
} as const satisfies Record<ScheduleInterval, string>;

function parseRecipients(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

// The /reports/scheduled page's client half: a create form (name, report
// definition select, schedule interval select, comma-separated recipient
// user IDs, active toggle), per-row inline edit (same form shape
// reused), and delete - mirrors components/Settings/WebhooksManager.tsx's
// own shape closely (same owner-gated CRUD tier). A plain comma-separated
// UUID input for recipients, per the design brief, rather than a new
// firm-member picker component - no existing cheap-to-reuse picker was
// found elsewhere in this codebase (webhooks/notifications don't have
// one either).
export default function ScheduledReportsManager({ firmId, scheduledReports, definitions, locale }: Props) {
  const t = useTranslations("ScheduledReports");
  const router = useRouter();

  const [name, setName] = useState("");
  const [definitionId, setDefinitionId] = useState(definitions[0]?.id ?? "");
  const [interval, setInterval] = useState<ScheduleInterval>("weekly");
  const [recipients, setRecipients] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editDefinitionId, setEditDefinitionId] = useState("");
  const [editInterval, setEditInterval] = useState<ScheduleInterval>("weekly");
  const [editRecipients, setEditRecipients] = useState("");
  const [editActive, setEditActive] = useState(true);
  const [editError, setEditError] = useState<string | null>(null);
  const [savingId, setSavingId] = useState<string | null>(null);

  const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ id: string; message: string } | null>(null);

  function definitionName(id: string): string {
    return definitions.find((d) => d.id === id)?.name ?? id;
  }

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim()) {
      setCreateError(t("createNameRequired"));
      return;
    }
    if (!definitionId) {
      setCreateError(t("createDefinitionRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const input: ScheduledReportInput = {
        name: name.trim(),
        definitionId,
        scheduleInterval: interval,
        recipientUserIds: parseRecipients(recipients),
        isActive: true,
      };
      const res = await fetch(`/api/scheduled-reports/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setName("");
      setRecipients("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  function startEdit(s: ScheduledReport) {
    setEditingId(s.id);
    setEditName(s.name);
    setEditDefinitionId(s.definitionId);
    setEditInterval(s.scheduleInterval);
    setEditRecipients(s.recipientUserIds.join(", "));
    setEditActive(s.isActive);
    setEditError(null);
  }

  function cancelEdit() {
    setEditingId(null);
    setEditError(null);
  }

  async function submitEdit(e: FormEvent, id: string) {
    e.preventDefault();
    setEditError(null);
    if (!editName.trim() || !editDefinitionId) {
      setEditError(t("createNameRequired"));
      return;
    }

    setSavingId(id);
    try {
      const input: ScheduledReportInput = {
        name: editName.trim(),
        definitionId: editDefinitionId,
        scheduleInterval: editInterval,
        recipientUserIds: parseRecipients(editRecipients),
        isActive: editActive,
      };
      const res = await fetch(`/api/scheduled-reports/${firmId}/${id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      });
      if (!res.ok) {
        setEditError(t("saveError"));
        return;
      }
      setEditingId(null);
      router.refresh();
    } catch {
      setEditError(t("saveError"));
    } finally {
      setSavingId(null);
    }
  }

  async function handleDelete(id: string) {
    setRowError(null);
    setPendingDeleteId(id);
    try {
      const res = await fetch(`/api/scheduled-reports/${firmId}/${id}`, { method: "DELETE" });
      if (!res.ok) {
        setRowError({ id, message: t("deleteError") });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ id, message: t("deleteError") });
    } finally {
      setPendingDeleteId(null);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <form
        onSubmit={submitCreate}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newTitle")}</h2>
        <label className="flex flex-col gap-1 text-sm">
          {t("name")}
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>
        <label className="flex flex-col gap-1 text-sm">
          {t("reportDefinition")}
          <select
            value={definitionId}
            onChange={(e) => setDefinitionId(e.target.value)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          >
            {definitions.length === 0 && <option value="">{t("reportDefinitionNone")}</option>}
            {definitions.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-sm">
          {t("scheduleInterval")}
          <select
            value={interval}
            onChange={(e) => setInterval(e.target.value as ScheduleInterval)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          >
            {INTERVALS.map((i) => (
              <option key={i} value={i}>
                {t(INTERVAL_KEYS[i])}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-sm">
          {t("recipients")}
          <input
            value={recipients}
            onChange={(e) => setRecipients(e.target.value)}
            placeholder={t("recipientsHelp")}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>

        {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
        <button
          type="submit"
          data-permission-public="true"
          disabled={submitting || definitions.length === 0}
          className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
        >
          {t("create")}
        </button>
      </form>

      {scheduledReports.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="flex flex-col gap-3">
          {scheduledReports.map((s) => (
            <div key={s.id} className="rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
              {editingId === s.id ? (
                <form onSubmit={(e) => submitEdit(e, s.id)} data-permission-public="true" className="flex flex-col gap-3">
                  <label className="flex flex-col gap-1 text-sm">
                    {t("name")}
                    <input
                      value={editName}
                      onChange={(e) => setEditName(e.target.value)}
                      className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                    />
                  </label>
                  <label className="flex flex-col gap-1 text-sm">
                    {t("reportDefinition")}
                    <select
                      value={editDefinitionId}
                      onChange={(e) => setEditDefinitionId(e.target.value)}
                      className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                    >
                      {definitions.map((d) => (
                        <option key={d.id} value={d.id}>
                          {d.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="flex flex-col gap-1 text-sm">
                    {t("scheduleInterval")}
                    <select
                      value={editInterval}
                      onChange={(e) => setEditInterval(e.target.value as ScheduleInterval)}
                      className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                    >
                      {INTERVALS.map((i) => (
                        <option key={i} value={i}>
                          {t(INTERVAL_KEYS[i])}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="flex flex-col gap-1 text-sm">
                    {t("recipients")}
                    <input
                      value={editRecipients}
                      onChange={(e) => setEditRecipients(e.target.value)}
                      placeholder={t("recipientsHelp")}
                      className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                    />
                  </label>
                  <label className="flex items-center gap-2 text-sm">
                    <input type="checkbox" checked={editActive} onChange={(e) => setEditActive(e.target.checked)} />
                    {t("active")}
                  </label>
                  {editError && <p className="text-sm text-red-600 dark:text-red-400">{editError}</p>}
                  <div className="flex gap-2">
                    <button
                      type="submit"
                      data-permission-public="true"
                      disabled={savingId === s.id}
                      className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
                    >
                      {t("save")}
                    </button>
                    <button
                      type="button"
                      data-permission-public="true"
                      onClick={cancelEdit}
                      className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                    >
                      {t("cancel")}
                    </button>
                  </div>
                </form>
              ) : (
                <div className="flex flex-col gap-2">
                  <div className="flex items-start justify-between gap-4">
                    <div className="flex flex-col gap-1">
                      <span className="font-semibold text-black dark:text-zinc-50">{s.name}</span>
                      <span className="text-xs text-zinc-600 dark:text-zinc-400">{definitionName(s.definitionId)}</span>
                      <span className="text-xs text-zinc-500 dark:text-zinc-500">
                        {t(`interval${s.scheduleInterval.charAt(0).toUpperCase()}${s.scheduleInterval.slice(1)}` as
                          | "intervalDaily"
                          | "intervalWeekly"
                          | "intervalMonthly")}{" "}
                        · {t("status")}: {s.isActive ? t("active") : t("disabled")}
                      </span>
                      <span className="text-xs text-zinc-500 dark:text-zinc-500">
                        {t("lastRun")}: {s.lastRunAt ? new Date(s.lastRunAt).toLocaleString(locale) : t("never")} ·{" "}
                        {t("nextRun")}: {s.nextRunAt ? new Date(s.nextRunAt).toLocaleString(locale) : t("never")}
                      </span>
                      <span className="text-xs text-zinc-500 dark:text-zinc-500">
                        {t("recipientsCount", { count: s.recipientUserIds.length })}
                      </span>
                    </div>
                    <div className="flex shrink-0 flex-col gap-1">
                      <button
                        type="button"
                        data-permission-public="true"
                        onClick={() => startEdit(s)}
                        className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                      >
                        {t("edit")}
                      </button>
                      <button
                        type="button"
                        data-permission-public="true"
                        disabled={pendingDeleteId === s.id}
                        onClick={() => handleDelete(s.id)}
                        className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
                      >
                        {t("delete")}
                      </button>
                    </div>
                  </div>
                  {rowError?.id === s.id && <p className="text-xs text-red-600 dark:text-red-400">{rowError.message}</p>}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
