"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { CompareOp, QueryFilter, QueryMetric, ReportRow } from "@/lib/reports";

type Props = {
  firmId: string;
};

// entityFields mirrors internal/reports.entityRegistry's own field
// allow-list, for this form's select options only - the backend's own
// registry (internal/reports/queryspec.go) is the real safety boundary;
// this list only has to be a reasonable UI convenience, never a security
// control, since BuildQuery rejects anything not on ITS list regardless
// of what this form ever sends.
const ENTITY_FIELDS: Record<string, string[]> = {
  workflow_instances: ["id", "workflow_definition_id", "created_by_user_id", "created_at", "updated_at"],
  journal_entries: ["id", "description", "source_type", "source_id", "created_by", "posted_at", "created_at"],
  invoices: [
    "id", "invoice_number", "customer_id", "status", "subtotal", "tax_amount", "total", "currency",
    "issued_date", "due_date", "created_at",
  ],
  deliveries: ["id", "reference", "carrier", "status", "source_type", "source_id", "estimated_date", "actual_date", "created_at"],
  people: ["id", "full_name", "type", "email", "status", "start_date", "end_date", "created_at"],
  products: ["id", "sku", "name", "unit", "unit_price", "cost_price", "category", "is_active", "created_at"],
};

const NUMERIC_FIELDS: Record<string, string[]> = {
  workflow_instances: [],
  journal_entries: [],
  invoices: ["subtotal", "tax_amount", "total"],
  deliveries: [],
  people: [],
  products: ["unit_price", "cost_price"],
};

const OPS: CompareOp[] = ["eq", "neq", "lt", "gt", "lte", "gte", "contains"];

export default function ReportBuilder({ firmId }: Props) {
  const t = useTranslations("Reports");
  const router = useRouter();

  const [name, setName] = useState("");
  const [entity, setEntity] = useState("invoices");
  const [groupBy, setGroupBy] = useState("");
  const [metrics, setMetrics] = useState<QueryMetric[]>([{ name: "count", aggregation: "count" }]);
  const [filters, setFilters] = useState<QueryFilter[]>([]);
  const [dateField, setDateField] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");

  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [rows, setRows] = useState<ReportRow[] | null>(null);

  const fields = ENTITY_FIELDS[entity] ?? [];
  const numericFields = NUMERIC_FIELDS[entity] ?? [];

  function addMetric() {
    setMetrics((prev) => [...prev, { name: "", aggregation: "count" }]);
  }
  function updateMetric(idx: number, patch: Partial<QueryMetric>) {
    setMetrics((prev) => prev.map((m, i) => (i === idx ? { ...m, ...patch } : m)));
  }
  function removeMetric(idx: number) {
    setMetrics((prev) => prev.filter((_, i) => i !== idx));
  }

  function addFilter() {
    setFilters((prev) => [...prev, { field: fields[0] ?? "", op: "eq", value: "" }]);
  }
  function updateFilter(idx: number, patch: Partial<QueryFilter>) {
    setFilters((prev) => prev.map((f, i) => (i === idx ? { ...f, ...patch } : f)));
  }
  function removeFilter(idx: number) {
    setFilters((prev) => prev.filter((_, i) => i !== idx));
  }

  async function saveAndRun() {
    setError(null);
    if (!name.trim() || metrics.length === 0) {
      setError(t("builderRequired"));
      return;
    }

    setSaving(true);
    setRows(null);
    try {
      const createRes = await fetch(`/api/reports/definitions/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: name.trim(),
          querySpec: {
            entity,
            group_by: groupBy || undefined,
            metrics,
            filters: filters.length > 0 ? filters : undefined,
            date_range: dateField ? { field: dateField, from: dateFrom || undefined, to: dateTo || undefined } : undefined,
          },
        }),
      });
      if (!createRes.ok) {
        setError(t("builderSaveError"));
        return;
      }
      const definition = (await createRes.json()) as { id: string };

      const runRes = await fetch(`/api/reports/definitions/${firmId}/${definition.id}/run`, { method: "POST" });
      if (!runRes.ok) {
        setError(t("runError"));
        return;
      }
      setRows((await runRes.json()) as ReportRow[]);
      router.refresh();
    } catch {
      setError(t("builderSaveError"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <div className="flex flex-col gap-4 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
        <label className="flex flex-col gap-1 text-sm">
          {t("builderName")}
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>

        <label className="flex flex-col gap-1 text-sm">
          {t("builderEntity")}
          <select
            value={entity}
            onChange={(e) => {
              setEntity(e.target.value);
              setGroupBy("");
              setFilters([]);
              setDateField("");
            }}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          >
            {Object.keys(ENTITY_FIELDS).map((e) => (
              <option key={e} value={e}>
                {e}
              </option>
            ))}
          </select>
        </label>

        <label className="flex flex-col gap-1 text-sm">
          {t("builderGroupBy")}
          <select
            value={groupBy}
            onChange={(e) => setGroupBy(e.target.value)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          >
            <option value="">{t("builderNone")}</option>
            {fields.map((f) => (
              <option key={f} value={f}>
                {f}
              </option>
            ))}
          </select>
        </label>

        <div className="flex flex-col gap-2">
          <span className="text-sm font-medium text-black dark:text-zinc-50">{t("builderMetrics")}</span>
          {metrics.map((m, idx) => (
            <div key={idx} className="flex flex-wrap items-center gap-2">
              <input
                value={m.name}
                onChange={(e) => updateMetric(idx, { name: e.target.value })}
                placeholder={t("builderMetricName")}
                className="w-32 rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
              <select
                value={m.aggregation}
                onChange={(e) => updateMetric(idx, { aggregation: e.target.value })}
                className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="count">count</option> {/* i18n-ignore: a QuerySpec aggregation keyword, not UI prose */}
                {numericFields.map((f) => (
                  <optgroup key={f} label={f}>
                    <option value={`sum(${f})`}>{`sum(${f})`}</option>
                    <option value={`avg(${f})`}>{`avg(${f})`}</option>
                    <option value={`min(${f})`}>{`min(${f})`}</option>
                    <option value={`max(${f})`}>{`max(${f})`}</option>
                  </optgroup>
                ))}
              </select>
              <button
                type="button"
                data-permission-public="true"
                onClick={() => removeMetric(idx)}
                className="text-xs text-red-600 hover:underline dark:text-red-400"
              >
                {t("builderRemove")}
              </button>
            </div>
          ))}
          <button
            type="button"
            data-permission-public="true"
            onClick={addMetric}
            className="self-start rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
          >
            {t("builderAddMetric")}
          </button>
        </div>

        <div className="flex flex-col gap-2">
          <span className="text-sm font-medium text-black dark:text-zinc-50">{t("builderFilters")}</span>
          {filters.map((f, idx) => (
            <div key={idx} className="flex flex-wrap items-center gap-2">
              <select
                value={f.field}
                onChange={(e) => updateFilter(idx, { field: e.target.value })}
                className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {fields.map((field) => (
                  <option key={field} value={field}>
                    {field}
                  </option>
                ))}
              </select>
              <select
                value={f.op}
                onChange={(e) => updateFilter(idx, { op: e.target.value as CompareOp })}
                className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {OPS.map((op) => (
                  <option key={op} value={op}>
                    {op}
                  </option>
                ))}
              </select>
              <input
                value={String(f.value)}
                onChange={(e) => updateFilter(idx, { value: e.target.value })}
                className="w-32 rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
              <button
                type="button"
                data-permission-public="true"
                onClick={() => removeFilter(idx)}
                className="text-xs text-red-600 hover:underline dark:text-red-400"
              >
                {t("builderRemove")}
              </button>
            </div>
          ))}
          <button
            type="button"
            data-permission-public="true"
            onClick={addFilter}
            className="self-start rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
          >
            {t("builderAddFilter")}
          </button>
        </div>

        <div className="flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-1 text-sm">
            {t("builderDateField")}
            <select
              value={dateField}
              onChange={(e) => setDateField(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            >
              <option value="">{t("builderNone")}</option>
              {fields.map((f) => (
                <option key={f} value={f}>
                  {f}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("fromLabel")}
            <input
              type="date"
              value={dateFrom}
              onChange={(e) => setDateFrom(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("toLabel")}
            <input
              type="date"
              value={dateTo}
              onChange={(e) => setDateTo(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
        </div>

        {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

        <button
          type="button"
          data-permission-public="true"
          disabled={saving}
          onClick={saveAndRun}
          className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
        >
          {saving ? t("running") : t("builderSaveAndRun")}
        </button>
      </div>

      {rows && (
        <div className="overflow-x-auto rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                {groupBy && <th className="py-2 pr-4 font-medium">{t("groupBy")}</th>}
                {metrics.map((m) => (
                  <th key={m.name} className="py-2 pr-4 font-medium">
                    {m.name}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 ? (
                <tr>
                  <td className="py-2 text-zinc-500 dark:text-zinc-500" colSpan={metrics.length + 1}>
                    {t("noResults")}
                  </td>
                </tr>
              ) : (
                rows.map((row, idx) => (
                  <tr key={idx} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
                    {groupBy && <td className="py-2 pr-4 font-mono">{row.groupKey ?? "—"}</td>}
                    {metrics.map((m) => (
                      <td key={m.name} className="py-2 pr-4 font-mono">
                        {String(row.metrics[m.name] ?? "")}
                      </td>
                    ))}
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
