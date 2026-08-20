"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { AlertEvent } from "@/lib/alerts";
import type { AlertMetric, AlertRule, AlertRuleInput } from "@/lib/alertRules";

type Props = {
  rules: AlertRule[];
  events: AlertEvent[];
};

const METRICS: AlertMetric[] = ["error_rate", "response_time_p95", "volume_drop"];

const EMPTY_FORM: AlertRuleInput = {
  name: "",
  metric: "error_rate",
  threshold: 0,
  windowMinutes: 60,
  isActive: true,
};

// The /platform-admin/alerts page's rule list plus a create form, and an
// inline edit form per row - mirrors FirmGroupsManager.tsx's own
// "one form component owns create + per-row edit state" shape.
// Platform-admin-gated on the backend
// (internal/platformadmin.Allowlist via the alert-rules handlers); like
// ExchangeRatesManager.tsx/FirmGroupsManager.tsx, every interactive
// element here carries data-permission-public="true" rather than a
// data-permission-key, since there is no per-firm permission-catalog key
// for a platform-wide, allowlist-gated action.
export default function AlertRulesManager({ rules, events }: Props) {
  const t = useTranslations("PlatformAdminAlerts");
  const router = useRouter();

  const [form, setForm] = useState<AlertRuleInput>(EMPTY_FORM);
  const [createError, setCreateError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editForm, setEditForm] = useState<AlertRuleInput>(EMPTY_FORM);
  const [editError, setEditError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  function startEdit(rule: AlertRule) {
    setEditingId(rule.id);
    setEditForm({
      name: rule.name,
      metric: rule.metric,
      threshold: rule.threshold,
      windowMinutes: rule.windowMinutes,
      isActive: rule.isActive,
    });
    setEditError(null);
  }

  function cancelEdit() {
    setEditingId(null);
    setEditError(null);
  }

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!form.name.trim() || form.windowMinutes <= 0) {
      setCreateError(t("createRequired"));
      return;
    }
    setCreating(true);
    try {
      const res = await fetch(`/api/platform-admin/alert-rules`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...form, name: form.name.trim() }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setForm(EMPTY_FORM);
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setCreating(false);
    }
  }

  async function submitEdit(e: FormEvent, ruleId: string) {
    e.preventDefault();
    setEditError(null);
    if (!editForm.name.trim() || editForm.windowMinutes <= 0) {
      setEditError(t("createRequired"));
      return;
    }
    setSaving(true);
    try {
      const res = await fetch(`/api/platform-admin/alert-rules/${ruleId}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...editForm, name: editForm.name.trim() }),
      });
      if (!res.ok) {
        setEditError(t("updateError"));
        return;
      }
      setEditingId(null);
      router.refresh();
    } catch {
      setEditError(t("updateError"));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(ruleId: string) {
    setDeleteError(null);
    setDeletingId(ruleId);
    try {
      const res = await fetch(`/api/platform-admin/alert-rules/${ruleId}`, { method: "DELETE" });
      if (!res.ok) {
        setDeleteError(t("deleteError"));
        return;
      }
      router.refresh();
    } catch {
      setDeleteError(t("deleteError"));
    } finally {
      setDeletingId(null);
    }
  }

  return (
    <div className="flex w-full flex-col gap-8">
      <form
        onSubmit={submitCreate}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newRuleTitle")}</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
          <label className="flex flex-col gap-1 text-sm">
            {t("nameLabel")}
            <input
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("metricLabel")}
            <select
              value={form.metric}
              onChange={(e) => setForm((f) => ({ ...f, metric: e.target.value as AlertMetric }))}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            >
              {METRICS.map((m) => (
                <option key={m} value={m}>
                  {t(`metric.${m}`)}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("thresholdLabel")}
            <input
              type="number"
              step="any"
              value={form.threshold}
              onChange={(e) => setForm((f) => ({ ...f, threshold: Number(e.target.value) }))}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("windowMinutesLabel")}
            <input
              type="number"
              min={1}
              value={form.windowMinutes}
              onChange={(e) => setForm((f) => ({ ...f, windowMinutes: Number(e.target.value) }))}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={form.isActive}
            onChange={(e) => setForm((f) => ({ ...f, isActive: e.target.checked }))}
            data-permission-public="true"
          />
          {t("isActiveLabel")}
        </label>
        {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
        <button
          type="submit"
          data-permission-public="true"
          disabled={creating}
          className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
        >
          {t("create")}
        </button>
      </form>

      {deleteError && <p className="text-sm text-red-600 dark:text-red-400">{deleteError}</p>}

      {rules.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("nameLabel")}</th>
                <th className="py-2 pr-4 font-medium">{t("metricLabel")}</th>
                <th className="py-2 pr-4 font-medium">{t("thresholdLabel")}</th>
                <th className="py-2 pr-4 font-medium">{t("windowMinutesLabel")}</th>
                <th className="py-2 pr-4 font-medium">{t("isActiveLabel")}</th>
                <th className="py-2 font-medium">{t("actionsLabel")}</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule) => (
                <tr key={rule.id}>
                  {editingId === rule.id ? (
                    <td colSpan={6} className="py-2">
                      <form
                        onSubmit={(e) => submitEdit(e, rule.id)}
                        data-permission-public="true"
                        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-3 dark:border-zinc-700"
                      >
                        <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
                          <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                            {t("nameLabel")}
                            <input
                              value={editForm.name}
                              onChange={(e) => setEditForm((f) => ({ ...f, name: e.target.value }))}
                              className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                            />
                          </label>
                          <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                            {t("metricLabel")}
                            <select
                              value={editForm.metric}
                              onChange={(e) =>
                                setEditForm((f) => ({ ...f, metric: e.target.value as AlertMetric }))
                              }
                              className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                            >
                              {METRICS.map((m) => (
                                <option key={m} value={m}>
                                  {t(`metric.${m}`)}
                                </option>
                              ))}
                            </select>
                          </label>
                          <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                            {t("thresholdLabel")}
                            <input
                              type="number"
                              step="any"
                              value={editForm.threshold}
                              onChange={(e) =>
                                setEditForm((f) => ({ ...f, threshold: Number(e.target.value) }))
                              }
                              className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                            />
                          </label>
                          <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                            {t("windowMinutesLabel")}
                            <input
                              type="number"
                              min={1}
                              value={editForm.windowMinutes}
                              onChange={(e) =>
                                setEditForm((f) => ({ ...f, windowMinutes: Number(e.target.value) }))
                              }
                              className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                            />
                          </label>
                        </div>
                        <label className="flex items-center gap-2 text-xs text-zinc-600 dark:text-zinc-400">
                          <input
                            type="checkbox"
                            checked={editForm.isActive}
                            onChange={(e) => setEditForm((f) => ({ ...f, isActive: e.target.checked }))}
                            data-permission-public="true"
                          />
                          {t("isActiveLabel")}
                        </label>
                        {editError && <p className="text-sm text-red-600 dark:text-red-400">{editError}</p>}
                        <div className="flex gap-2">
                          <button
                            type="submit"
                            data-permission-public="true"
                            disabled={saving}
                            className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
                          >
                            {t("save")}
                          </button>
                          <button
                            type="button"
                            data-permission-public="true"
                            onClick={cancelEdit}
                            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm text-zinc-700 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-900"
                          >
                            {t("cancel")}
                          </button>
                        </div>
                      </form>
                    </td>
                  ) : (
                    <>
                      <td className="py-2 pr-4 text-black dark:text-zinc-50">{rule.name}</td>
                      <td className="py-2 pr-4 text-black dark:text-zinc-50">{t(`metric.${rule.metric}`)}</td>
                      <td className="py-2 pr-4 text-black dark:text-zinc-50">{rule.threshold}</td>
                      <td className="py-2 pr-4 text-black dark:text-zinc-50">{rule.windowMinutes}</td>
                      <td className="py-2 pr-4 text-black dark:text-zinc-50">
                        {rule.isActive ? t("active") : t("inactive")}
                      </td>
                      <td className="py-2">
                        <div className="flex gap-2">
                          <button
                            type="button"
                            data-permission-public="true"
                            onClick={() => startEdit(rule)}
                            className="rounded-md border border-zinc-300 px-2 py-1 text-xs text-zinc-700 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-900"
                          >
                            {t("edit")}
                          </button>
                          <button
                            type="button"
                            data-permission-public="true"
                            disabled={deletingId === rule.id}
                            onClick={() => handleDelete(rule.id)}
                            className="rounded-md border border-red-300 px-2 py-1 text-xs text-red-700 hover:bg-red-50 disabled:opacity-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950"
                          >
                            {t("delete")}
                          </button>
                        </div>
                      </td>
                    </>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <section>
        <h2 className="mb-3 text-lg font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("recentEventsTitle")}
        </h2>
        {events.length === 0 ? (
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("recentEventsEmpty")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium">{t("eventRuleLabel")}</th>
                  <th className="py-2 pr-4 font-medium">{t("eventTriggeredAtLabel")}</th>
                  <th className="py-2 font-medium">{t("eventMetricValueLabel")}</th>
                </tr>
              </thead>
              <tbody>
                {events.map((event) => (
                  <tr
                    key={event.id}
                    className="border-b border-zinc-200 text-black last:border-0 dark:border-zinc-800 dark:text-zinc-50"
                  >
                    <td className="py-2 pr-4">{event.ruleName}</td>
                    <td className="py-2 pr-4 whitespace-nowrap">
                      {new Date(event.triggeredAt).toLocaleString()}
                    </td>
                    <td className="py-2">{event.metricValue}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
