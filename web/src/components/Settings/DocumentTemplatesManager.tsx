"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { DocumentTemplate, DocumentTemplateType } from "@/lib/documents";

type Props = {
  firmId: string;
  templates: DocumentTemplate[];
};

const TEMPLATE_TYPES: DocumentTemplateType[] = ["invoice", "delivery_note", "report"];

// The /settings/document-templates page's client half: a create form (a
// plain textarea for the template body, not a visual editor - explicitly
// out of scope) and a per-row editor that PUTs updates, mirroring
// components/Settings/TaxRatesManager.tsx's own shape.
export default function DocumentTemplatesManager({ firmId, templates }: Props) {
  const t = useTranslations("DocumentTemplatesSettings");
  const router = useRouter();

  const [name, setName] = useState("");
  const [type, setType] = useState<DocumentTemplateType>("invoice");
  const [body, setBody] = useState("");
  const [isDefault, setIsDefault] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editBody, setEditBody] = useState("");
  const [editError, setEditError] = useState<string | null>(null);
  const [savingId, setSavingId] = useState<string | null>(null);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim() || !body.trim()) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/documents/templates/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name.trim(), type, template: body, isDefault }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setName("");
      setBody("");
      setIsDefault(false);
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  function startEdit(tpl: DocumentTemplate) {
    setEditingId(tpl.id);
    setEditBody(tpl.template);
    setEditError(null);
  }

  async function saveEdit(tpl: DocumentTemplate) {
    setEditError(null);
    setSavingId(tpl.id);
    try {
      const res = await fetch(`/api/documents/templates/${firmId}/${tpl.id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: tpl.name, type: tpl.type, template: editBody, isDefault: tpl.isDefault }),
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

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <form
        onSubmit={submitCreate}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newTemplateTitle")}</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="flex flex-col gap-1 text-sm">
            {t("name")}
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("typeLabel")}
            <select
              value={type}
              onChange={(e) => setType(e.target.value as DocumentTemplateType)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            >
              {TEMPLATE_TYPES.map((typ) => (
                <option key={typ} value={typ}>
                  {t(`type.${typ}`)}
                </option>
              ))}
            </select>
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={isDefault} onChange={(e) => setIsDefault(e.target.checked)} />
            {t("isDefault")}
          </label>
        </div>
        <label className="flex flex-col gap-1 text-sm">
          {t("templateBody")}
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            rows={8}
            className="rounded-md border border-zinc-300 px-2 py-1.5 font-mono text-xs dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>
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

      {templates.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="flex flex-col gap-3">
          {templates.map((tpl) => (
            <div key={tpl.id} className="flex flex-col gap-2 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <span className="font-medium text-black dark:text-zinc-50">{tpl.name}</span>{" "}
                  <span className="text-xs text-zinc-500 dark:text-zinc-400">
                    ({t(`type.${tpl.type}`)}
                    {tpl.isDefault ? `, ${t("isDefault")}` : ""})
                  </span>
                </div>
                {editingId === tpl.id ? (
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      data-permission-public="true"
                      disabled={savingId === tpl.id}
                      onClick={() => saveEdit(tpl)}
                      className="rounded-md bg-black px-2 py-1 text-xs font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
                    >
                      {t("save")}
                    </button>
                    <button
                      type="button"
                      data-permission-public="true"
                      onClick={() => setEditingId(null)}
                      className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                    >
                      {t("cancel")}
                    </button>
                  </div>
                ) : (
                  <button
                    type="button"
                    data-permission-public="true"
                    onClick={() => startEdit(tpl)}
                    className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                  >
                    {t("edit")}
                  </button>
                )}
              </div>
              {editingId === tpl.id ? (
                <>
                  <textarea
                    value={editBody}
                    onChange={(e) => setEditBody(e.target.value)}
                    rows={8}
                    className="rounded-md border border-zinc-300 px-2 py-1.5 font-mono text-xs dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                  />
                  {editError && <p className="text-xs text-red-600 dark:text-red-400">{editError}</p>}
                </>
              ) : (
                <pre className="max-h-40 overflow-auto rounded-md bg-zinc-100 p-2 text-xs text-zinc-700 dark:bg-zinc-900 dark:text-zinc-300">
                  {tpl.template}
                </pre>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
