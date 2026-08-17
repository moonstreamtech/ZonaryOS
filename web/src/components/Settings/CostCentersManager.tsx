"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { CostCenter } from "@/lib/costcenter";

type Props = {
  firmId: string;
  costCenters: CostCenter[];
};

// The /settings/cost-centers page's client half: a create form and a
// per-row "toggle active" action, mirroring components/Accounting/
// AccountsManager.tsx's own create-form/row-action shape for the same
// owner-gated "firm-structural CRUD, no dedicated admin panel" pattern.
// Code is immutable once created (same convention as Account.code) -
// this component never offers to edit it, only the active flag.
export default function CostCentersManager({ firmId, costCenters }: Props) {
  const t = useTranslations("CostCentersSettings");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [parentId, setParentId] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [pendingCostCenterId, setPendingCostCenterId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ costCenterId: string; message: string } | null>(null);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    const trimmedCode = code.trim();
    const trimmedName = name.trim();
    if (!trimmedCode) {
      setCreateError(t("createCodeRequired"));
      return;
    }
    if (!trimmedName) {
      setCreateError(t("createNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/cost-centers/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          code: trimmedCode,
          name: trimmedName,
          parentId: parentId || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(res.status === 409 ? t("createCodeExists") : t("createError"));
        return;
      }
      setCreating(false);
      setCode("");
      setName("");
      setParentId("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function toggleActive(costCenter: CostCenter) {
    setRowError(null);
    setPendingCostCenterId(costCenter.id);
    try {
      const res = await fetch(`/api/cost-centers/${firmId}/${costCenter.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ isActive: !costCenter.isActive }),
      });
      if (!res.ok) {
        setRowError({ costCenterId: costCenter.id, message: t("updateError") });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ costCenterId: costCenter.id, message: t("updateError") });
    } finally {
      setPendingCostCenterId(null);
    }
  }

  const nameById = Object.fromEntries(costCenters.map((c) => [c.id, c.name]));

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      <div className="flex items-center justify-end">
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setCreating((v) => !v)}
          className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
        >
          {creating ? t("cancel") : t("newCostCenter")}
        </button>
      </div>

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("code")}
              <input
                value={code}
                onChange={(e) => setCode(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("name")}
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("parentCostCenter")}
              <select
                value={parentId}
                onChange={(e) => setParentId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("noParent")}</option>
                {costCenters.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.code} — {c.name}
                  </option>
                ))}
              </select>
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

      {costCenters.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("code")}</th>
                <th className="py-2 pr-4 font-medium">{t("name")}</th>
                <th className="py-2 pr-4 font-medium">{t("parentCostCenter")}</th>
                <th className="py-2 pr-4 font-medium">{t("status")}</th>
                <th className="py-2 font-medium">{t("actions")}</th>
              </tr>
            </thead>
            <tbody>
              {costCenters.map((c) => (
                <tr
                  key={c.id}
                  className="border-b border-zinc-200 align-top text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4 font-mono text-xs whitespace-nowrap">{c.code}</td>
                  <td className="py-2 pr-4">{c.name}</td>
                  <td className="py-2 pr-4">{c.parentId ? (nameById[c.parentId] ?? "—") : "—"}</td>
                  <td className="py-2 pr-4">{c.isActive ? t("statusActive") : t("statusInactive")}</td>
                  <td className="py-2">
                    <button
                      type="button"
                      data-permission-public="true"
                      disabled={pendingCostCenterId === c.id}
                      onClick={() => toggleActive(c)}
                      className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                    >
                      {c.isActive ? t("deactivate") : t("activate")}
                    </button>
                    {rowError?.costCenterId === c.id && (
                      <p className="mt-1 text-xs text-red-600 dark:text-red-400">{rowError.message}</p>
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
