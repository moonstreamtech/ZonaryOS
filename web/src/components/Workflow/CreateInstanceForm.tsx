"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useEffect, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { FieldSpecInput } from "@/lib/workflow";
import type { Person } from "@/lib/hr";
import type { Product, Supplier } from "@/lib/inventory";
import {
  arrayItemDefault,
  buildFreeformPayload,
  buildTypedPayload,
  hasSchema as computeHasSchema,
  typedFieldDefault,
  type ArrayRowValue,
  type TypedFieldValue,
} from "./createInstancePayload";

type Props = {
  firmId: string;
  definitionId: string;
  createPermissionKey: string;
  // Fields is OPTIONAL (Open Points item 35) - when the target definition
  // carries a payload schema, this form renders one typed input per
  // field instead of the freeform key/value editor below. Undefined or
  // empty (every definition that predates item 35, e.g. stock_to_sale/
  // customer_pipeline today) falls back to exactly the freeform behavior
  // this component always had.
  fields?: FieldSpecInput[];
  // people is the firm's real roster (internal/hr.ListPeople), fetched
  // server-side by WorkflowDefinitionView and handed down here the same
  // way DefinitionBuilder's own accounts prop is - only used to populate
  // a "person" field's <select> (see PersonPicker below). A plain
  // client-side prop rather than PersonPicker fetching its own data
  // (unlike ReferencePicker's search-as-you-type, which needs a
  // paginated backend query): HR rosters are small for any firm this
  // batch's scope targets, so there's no need for a second GET proxy
  // route just to filter a short list.
  people?: Person[];
  // products/suppliers are the firm's real catalogs (internal/inventory.ListProducts/
  // ListSuppliers), fetched server-side by WorkflowDefinitionView and
  // handed down here the same way `people` above is - only used to
  // populate a "product"/"supplier" field's <select> (see ProductPicker/
  // SupplierPicker below), same "small enough to fetch eagerly, no
  // second GET proxy route" reasoning `people`'s own doc comment gives.
  products?: Product[];
  suppliers?: Supplier[];
};

type FieldRow = { id: number; key: string; value: string };

let nextRowId = 0;
function emptyRow(): FieldRow {
  return { id: nextRowId++, key: "", value: "" };
}

// Generic instance-creation form: a free-form key/value field editor when
// the target definition has no payload schema - internal/workflow.CreateInstance's
// payload is an unshaped map[string]any in that case, so this remains the
// most reasonable fallback (works for any definition's payload shape with
// zero code changes per workflow) - or, when the definition *does* carry
// a payload schema (Open Points item 35/38), one typed input per declared
// field (a <select> for enum, a repeatable input group for array, a
// searchable picker for reference - see ReferencePicker below), with
// required fields marked and validated client-side before submit. Either
// way this is the same component/same submit path (POST .../instances) -
// not two parallel forms.
export default function CreateInstanceForm({
  firmId,
  definitionId,
  createPermissionKey,
  fields,
  people,
  products,
  suppliers,
}: Props) {
  const t = useTranslations("Workflow");
  const router = useRouter();
  const hasSchema = computeHasSchema(fields);

  // Freeform branch state - unchanged from before item 35.
  const [rows, setRows] = useState<FieldRow[]>([emptyRow()]);

  // Typed branch state - one entry per schema field, keyed by field name.
  const [typedValues, setTypedValues] = useState<Record<string, TypedFieldValue>>(
    () => Object.fromEntries((fields ?? []).map((f) => [f.name, typedFieldDefault(f)])),
  );

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

  function updateTypedValue(name: string, value: TypedFieldValue) {
    setTypedValues((prev) => ({ ...prev, [name]: value }));
  }

  function arrayRowsOf(name: string): ArrayRowValue[] {
    const v = typedValues[name];
    return Array.isArray(v) ? v : [];
  }

  function addArrayRow(field: FieldSpecInput) {
    updateTypedValue(field.name, [...arrayRowsOf(field.name), arrayItemDefault(field.arrayItemType)]);
  }

  function updateArrayRow(field: FieldSpecInput, index: number, value: ArrayRowValue) {
    const next = arrayRowsOf(field.name).slice();
    next[index] = value;
    updateTypedValue(field.name, next);
  }

  function removeArrayRow(field: FieldSpecInput, index: number) {
    updateTypedValue(
      field.name,
      arrayRowsOf(field.name).filter((_, i) => i !== index),
    );
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);

    const payload = hasSchema
      ? buildTypedPayload(fields ?? [], typedValues)
      : buildFreeformPayload(rows);
    if (payload === null) {
      setError(t("schemaValidationError"));
      return;
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
      setTypedValues(
        Object.fromEntries((fields ?? []).map((f) => [f.name, typedFieldDefault(f)])),
      );
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
      data-permission-key={createPermissionKey}
      className="flex w-full flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
    >
      <h2 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
        {t("addTitle")}
      </h2>

      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      {hasSchema ? (
        <div className="flex flex-wrap gap-3">
          {(fields ?? []).map((field) => (
            <div key={field.name} className="flex min-w-[8rem] flex-col gap-1">
              <label className="text-xs text-zinc-600 dark:text-zinc-400">
                {field.name}
                {field.required && (
                  <span
                    aria-hidden="true"
                    className="pl-0.5 text-red-600 dark:text-red-400"
                  >
                    {t("schemaRequiredMark")}
                  </span>
                )}
              </label>

              {field.type === "boolean" ? (
                <label className="flex items-center gap-1 pt-1.5 text-xs text-zinc-600 dark:text-zinc-400">
                  <input
                    type="checkbox"
                    checked={Boolean(typedValues[field.name])}
                    onChange={(e) => updateTypedValue(field.name, e.target.checked)}
                  />
                  {t("schemaCheckboxLabel")}
                </label>
              ) : field.type === "enum" ? (
                <select
                  value={typeof typedValues[field.name] === "string" ? (typedValues[field.name] as string) : ""}
                  onChange={(e) => updateTypedValue(field.name, e.target.value)}
                  required={field.required}
                  className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
                >
                  <option value="">{t("enumUnselectedOption")}</option>
                  {(field.options ?? []).map((opt) => (
                    <option key={opt} value={opt}>
                      {opt}
                    </option>
                  ))}
                </select>
              ) : field.type === "reference" ? (
                <ReferencePicker
                  firmId={firmId}
                  field={field}
                  value={typeof typedValues[field.name] === "string" ? (typedValues[field.name] as string) : ""}
                  onChange={(id) => updateTypedValue(field.name, id)}
                />
              ) : field.type === "person" ? (
                <select
                  value={typeof typedValues[field.name] === "string" ? (typedValues[field.name] as string) : ""}
                  onChange={(e) => updateTypedValue(field.name, e.target.value)}
                  required={field.required}
                  className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
                >
                  <option value="">{t("enumUnselectedOption")}</option>
                  {(people ?? []).map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.fullName}
                    </option>
                  ))}
                </select>
              ) : field.type === "product" ? (
                <select
                  value={typeof typedValues[field.name] === "string" ? (typedValues[field.name] as string) : ""}
                  onChange={(e) => updateTypedValue(field.name, e.target.value)}
                  required={field.required}
                  className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
                >
                  <option value="">{t("enumUnselectedOption")}</option>
                  {(products ?? []).map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.sku} - {p.name}
                    </option>
                  ))}
                </select>
              ) : field.type === "supplier" ? (
                <select
                  value={typeof typedValues[field.name] === "string" ? (typedValues[field.name] as string) : ""}
                  onChange={(e) => updateTypedValue(field.name, e.target.value)}
                  required={field.required}
                  className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
                >
                  <option value="">{t("enumUnselectedOption")}</option>
                  {(suppliers ?? []).map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
              ) : field.type === "array" ? (
                <div className="flex flex-col gap-1.5">
                  {arrayRowsOf(field.name).map((row, index) => (
                    <div key={index} className="flex items-center gap-1.5">
                      {field.arrayItemType === "boolean" ? (
                        <label className="flex items-center gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                          <input
                            type="checkbox"
                            checked={Boolean(row)}
                            onChange={(e) => updateArrayRow(field, index, e.target.checked)}
                          />
                          {t("schemaCheckboxLabel")}
                        </label>
                      ) : (
                        <input
                          type={
                            field.arrayItemType === "number"
                              ? "number"
                              : field.arrayItemType === "date"
                                ? "date"
                                : "text"
                          }
                          value={typeof row === "string" ? row : ""}
                          onChange={(e) => updateArrayRow(field, index, e.target.value)}
                          className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
                        />
                      )}
                      <button
                        type="button"
                        data-permission-public="true"
                        onClick={() => removeArrayRow(field, index)}
                        className="rounded-md border border-zinc-300 px-2 py-1 text-xs text-zinc-600 dark:border-zinc-700 dark:text-zinc-400"
                      >
                        {t("removeFieldButton")}
                      </button>
                    </div>
                  ))}
                  <button
                    type="button"
                    data-permission-public="true"
                    onClick={() => addArrayRow(field)}
                    className="self-start text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
                  >
                    {t("arrayAddRowButton")}
                  </button>
                </div>
              ) : (
                <input
                  type={
                    field.type === "number"
                      ? "number"
                      : field.type === "date"
                        ? "date"
                        : "text"
                  }
                  value={typeof typedValues[field.name] === "string" ? (typedValues[field.name] as string) : ""}
                  onChange={(e) => updateTypedValue(field.name, e.target.value)}
                  required={field.required}
                  className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
                />
              )}
            </div>
          ))}
        </div>
      ) : (
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
      )}

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

// ReferencePicker is a "reference" field's input (Open Points item 38): a
// search box over the referenced definition's own instances, reusing the
// exact same paged/searchable instance-list mechanism the rest of this
// app already uses (fetchInstancesPage/searchAcrossDefinitions,
// lib/workflow.ts) rather than a new lookup - see
// src/app/api/workflow/definitions/by-key/[key]/instances/route.ts's own
// doc comment for why a new proxy route was still needed (this is the
// first CLIENT-side caller of that fetch helper; every prior caller was a
// server component with its own session token already in scope). Typing
// in the search box re-queries (debounced) rather than filtering a
// client-side list, so this works the same whether the referenced
// definition has ten instances or ten thousand.
function ReferencePicker({
  firmId,
  field,
  value,
  onChange,
}: {
  firmId: string;
  field: FieldSpecInput;
  value: string;
  onChange: (instanceId: string) => void;
}) {
  const t = useTranslations("Workflow");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<{ instanceId: string; label: string }[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!field.referenceDefinitionKey) return;
    let cancelled = false;

    async function search() {
      setLoading(true);
      try {
        const params = new URLSearchParams({ firmId, limit: "10", offset: "0" });
        if (query) params.set("q", query);
        const res = await fetch(
          `/api/workflow/definitions/by-key/${encodeURIComponent(field.referenceDefinitionKey!)}/instances?${params.toString()}`,
        );
        const data = (res.ok ? await res.json() : { instances: [] }) as {
          instances?: { instanceId: string; state: { name: string } }[];
        };
        if (cancelled) return;
        setResults(
          (data.instances ?? []).map((inst) => ({
            instanceId: inst.instanceId,
            label: `${inst.instanceId.slice(0, 8)} - ${inst.state?.name ?? ""}`,
          })),
        );
      } catch {
        if (!cancelled) setResults([]);
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    void search();
    return () => {
      cancelled = true;
    };
  }, [firmId, field.referenceDefinitionKey, query]);

  return (
    <div className="flex flex-col gap-1">
      <input
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={t("referenceSearchPlaceholder")}
        aria-label={t("referenceSearchLabel", { key: field.referenceDefinitionKey ?? "" })}
        data-permission-public="true"
        className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
      />
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        required={field.required}
        className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
      >
        <option value="">{loading ? t("referenceLoading") : t("referenceUnselectedOption")}</option>
        {results.map((r) => (
          <option key={r.instanceId} value={r.instanceId}>
            {r.label}
          </option>
        ))}
      </select>
      {!loading && results.length === 0 && (
        <p className="text-xs text-zinc-500 dark:text-zinc-500">{t("referenceNoResults")}</p>
      )}
    </div>
  );
}
