"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";

type Props = {
  firmId: string;
  definitionId: string;
  createPermissionKey: string;
};

type FieldRow = { id: number; key: string; value: string };

let nextRowId = 0;
function emptyRow(): FieldRow {
  return { id: nextRowId++, key: "", value: "" };
}

// Generic replacement for the old stock-specific AddStockForm.tsx: a
// free-form key/value field editor rather than a fixed "item"/"quantity"
// field list, since internal/workflow.CreateInstance's payload is an
// unshaped map[string]any - there is no per-definition payload schema
// today (see docs/OPEN_POINTS.md's new "workflow instance payload
// schema" item, filed rather than guessed at one; Open Points item 12's
// wizard question-tree design is the eventual place that would produce
// one). This is the most reasonable fallback until that exists: works
// for any definition's payload shape, including stock_to_sale's
// item/quantity fields (typed in as two rows) and any future workflow's
// entirely different fields, with zero code changes per workflow.
export default function CreateInstanceForm({
  firmId,
  definitionId,
  createPermissionKey,
}: Props) {
  const t = useTranslations("Workflow");
  const router = useRouter();
  const [rows, setRows] = useState<FieldRow[]>([emptyRow()]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function updateRow(id: number, field: "key" | "value", text: string) {
    setRows((prev) =>
      prev.map((row) => (row.id === id ? { ...row, [field]: text } : row)),
    );
  }

  function addRow() {
    setRows((prev) => [...prev, emptyRow()]);
  }

  function removeRow(id: number) {
    setRows((prev) => (prev.length === 1 ? prev : prev.filter((r) => r.id !== id)));
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);

    const payload: Record<string, unknown> = {};
    for (const row of rows) {
      const key = row.key.trim();
      if (key === "") continue;
      const numeric = Number(row.value);
      payload[key] =
        row.value.trim() !== "" && Number.isFinite(numeric)
          ? numeric
          : row.value;
    }

    setSubmitting(true);
    try {
      const res = await fetch("/api/workflow/instances", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ firmId, definitionId, payload }),
      });
      if (!res.ok) {
        setError(
          res.status === 403 ? t("addPermissionDenied") : t("addError"),
        );
        return;
      }
      setRows([emptyRow()]);
      router.refresh();
    } catch {
      setError(t("addError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form
      onSubmit={handleSubmit}
      className="flex w-full flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
    >
      <h2 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
        {t("addTitle")}
      </h2>

      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="flex flex-col gap-2">
        {rows.map((row) => (
          <div key={row.id} className="flex flex-wrap items-end gap-2">
            <div className="flex flex-1 min-w-[8rem] flex-col gap-1">
              <label className="text-xs text-zinc-600 dark:text-zinc-400">
                {t("fieldKeyLabel")}
              </label>
              <input
                type="text"
                value={row.key}
                onChange={(e) => updateRow(row.id, "key", e.target.value)}
                placeholder={t("fieldKeyPlaceholder")}
                className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
              />
            </div>
            <div className="flex flex-1 min-w-[8rem] flex-col gap-1">
              <label className="text-xs text-zinc-600 dark:text-zinc-400">
                {t("fieldValueLabel")}
              </label>
              <input
                type="text"
                value={row.value}
                onChange={(e) => updateRow(row.id, "value", e.target.value)}
                placeholder={t("fieldValuePlaceholder")}
                className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
              />
            </div>
            <button
              type="button"
              data-permission-public="true"
              disabled={rows.length === 1}
              onClick={() => removeRow(row.id)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 text-xs text-zinc-600 disabled:opacity-30 dark:border-zinc-700 dark:text-zinc-400"
            >
              {t("removeFieldButton")}
            </button>
          </div>
        ))}

        <button
          type="button"
          data-permission-public="true"
          onClick={addRow}
          className="self-start text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
        >
          {t("addFieldButton")}
        </button>
      </div>

      <button
        type="submit"
        data-permission-key={createPermissionKey}
        disabled={submitting}
        className="self-start rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] disabled:opacity-50 dark:hover:bg-[#ccc]"
      >
        {t("addSubmit")}
      </button>
    </form>
  );
}
