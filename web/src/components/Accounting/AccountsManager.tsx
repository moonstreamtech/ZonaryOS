"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Account, AccountType } from "@/lib/accounting";

type Props = {
  firmId: string;
  accounts: Account[];
};

const ACCOUNT_TYPES: AccountType[] = ["asset", "liability", "equity", "revenue", "expense"];

// The chart-of-accounts management page's client half: a create form and
// a per-row "toggle active" action, mirroring
// components/AuditMode/RoleManager.tsx's own create-form/row-action shape
// for the same owner-gated "firm-structural CRUD, no dedicated admin
// panel" pattern. Code and type are immutable once created (see
// internal/accounting.AccountUpdate's own doc comment) - this component
// never offers to edit them, only name and the active flag.
export default function AccountsManager({ firmId, accounts }: Props) {
  const t = useTranslations("AccountsSettings");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [type, setType] = useState<AccountType>("asset");
  const [parentId, setParentId] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [pendingAccountId, setPendingAccountId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ accountId: string; message: string } | null>(null);

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
      const res = await fetch(`/api/accounting/accounts/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          code: trimmedCode,
          name: trimmedName,
          type,
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
      setType("asset");
      setParentId("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function toggleActive(account: Account) {
    setRowError(null);
    setPendingAccountId(account.id);
    try {
      const res = await fetch(`/api/accounting/accounts/${firmId}/${account.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ isActive: !account.isActive }),
      });
      if (!res.ok) {
        setRowError({ accountId: account.id, message: t("updateError") });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ accountId: account.id, message: t("updateError") });
    } finally {
      setPendingAccountId(null);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("chartTitle")}
        </h2>
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setCreating((v) => !v)}
          className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
        >
          {creating ? t("cancel") : t("newAccount")}
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
            <label className="flex flex-col gap-1 text-sm">
              {t("type")}
              <select
                value={type}
                onChange={(e) => setType(e.target.value as AccountType)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {ACCOUNT_TYPES.map((accountType) => (
                  <option key={accountType} value={accountType}>
                    {t(`accountType.${accountType}`)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("parentAccount")}
              <select
                value={parentId}
                onChange={(e) => setParentId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("noParent")}</option>
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.code} — {a.name}
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

      {accounts.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("code")}</th>
                <th className="py-2 pr-4 font-medium">{t("name")}</th>
                <th className="py-2 pr-4 font-medium">{t("type")}</th>
                <th className="py-2 pr-4 font-medium">{t("currency")}</th>
                <th className="py-2 pr-4 font-medium">{t("status")}</th>
                <th className="py-2 font-medium">{t("actions")}</th>
              </tr>
            </thead>
            <tbody>
              {accounts.map((a) => (
                <tr
                  key={a.id}
                  className="border-b border-zinc-200 align-top text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4 font-mono text-xs whitespace-nowrap">{a.code}</td>
                  <td className="py-2 pr-4">{a.name}</td>
                  <td className="py-2 pr-4">{t(`accountType.${a.type}`)}</td>
                  <td className="py-2 pr-4">{a.currency}</td>
                  <td className="py-2 pr-4">
                    {a.isActive ? t("statusActive") : t("statusInactive")}
                  </td>
                  <td className="py-2">
                    <button
                      type="button"
                      data-permission-public="true"
                      disabled={pendingAccountId === a.id}
                      onClick={() => toggleActive(a)}
                      className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                    >
                      {a.isActive ? t("deactivate") : t("activate")}
                    </button>
                    {rowError?.accountId === a.id && (
                      <p className="mt-1 text-xs text-red-600 dark:text-red-400">
                        {rowError.message}
                      </p>
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
