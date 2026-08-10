"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { APIKey } from "@/lib/apikeys";

type Props = {
  firmId: string;
  apiKeys: APIKey[];
  availableScopes: string[];
};

// The /settings/api-keys page's client half: a create form (name +
// scope checkboxes, drawn from availableScopes - see page.tsx's own doc
// comment) and a per-row revoke action, mirroring
// components/Settings/DocumentTemplatesManager.tsx's own shape. The
// one-time plaintext secret (createdKey) renders in an inline modal
// right after a successful create and is never stored in this
// component's own state beyond that - closing the modal discards it for
// good, matching internal/apikey.CreateAPIKey's own "never have the
// plaintext again after this call returns" guarantee server-side.
export default function ApiKeysManager({ firmId, apiKeys, availableScopes }: Props) {
  const t = useTranslations("ApiKeysSettings");
  const router = useRouter();

  const [name, setName] = useState("");
  const [selectedScopes, setSelectedScopes] = useState<string[]>([]);
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [createdKey, setCreatedKey] = useState<{ name: string; key: string } | null>(null);

  const [pendingKeyId, setPendingKeyId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ keyId: string; message: string } | null>(null);

  function toggleScope(scope: string) {
    setSelectedScopes((prev) => (prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope]));
  }

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim()) {
      setCreateError(t("createNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/apikeys/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name.trim(), scopes: selectedScopes }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      const created = (await res.json()) as APIKey;
      if (created.key) {
        setCreatedKey({ name: created.name, key: created.key });
      }
      setName("");
      setSelectedScopes([]);
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleRevoke(keyId: string) {
    setRowError(null);
    setPendingKeyId(keyId);
    try {
      const res = await fetch(`/api/apikeys/${firmId}/${keyId}`, { method: "DELETE" });
      if (!res.ok) {
        setRowError({ keyId, message: t("revokeError") });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ keyId, message: t("revokeError") });
    } finally {
      setPendingKeyId(null);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      {createdKey && (
        <div
          role="dialog"
          aria-modal="true"
          className="flex flex-col gap-3 rounded-md border border-amber-400 bg-amber-50 p-4 dark:border-amber-700 dark:bg-amber-950"
        >
          <h2 className="text-sm font-semibold text-black dark:text-zinc-50">
            {t("secretModalTitle", { name: createdKey.name })}
          </h2>
          <p className="text-xs text-zinc-700 dark:text-zinc-300">{t("secretModalBody")}</p>
          <code className="break-all rounded-md bg-white px-2 py-1.5 font-mono text-xs text-black dark:bg-black dark:text-zinc-50">
            {createdKey.key}
          </code>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreatedKey(null)}
            className="self-start rounded-md bg-black px-3 py-1.5 text-xs font-medium text-white dark:bg-zinc-50 dark:text-black"
          >
            {t("secretModalClose")}
          </button>
        </div>
      )}

      <form
        onSubmit={submitCreate}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newKeyTitle")}</h2>
        <label className="flex flex-col gap-1 text-sm">
          {t("name")}
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>

        <div className="flex flex-col gap-1">
          <span className="text-sm">{t("scopes")}</span>
          {availableScopes.length === 0 ? (
            <p className="text-xs text-zinc-500 dark:text-zinc-400">{t("noScopesAvailable")}</p>
          ) : (
            <div className="grid grid-cols-1 gap-1 sm:grid-cols-2">
              {availableScopes.map((scope) => (
                <label key={scope} className="flex items-center gap-2 text-xs">
                  <input
                    type="checkbox"
                    checked={selectedScopes.includes(scope)}
                    onChange={() => toggleScope(scope)}
                  />
                  <span className="font-mono">{scope}</span>
                </label>
              ))}
            </div>
          )}
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

      {apiKeys.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("name")}</th>
                <th className="py-2 pr-4 font-medium">{t("scopes")}</th>
                <th className="py-2 pr-4 font-medium">{t("status")}</th>
                <th className="py-2 pr-4 font-medium">{t("lastUsed")}</th>
                <th className="py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {apiKeys.map((k) => (
                <tr key={k.id} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
                  <td className="py-2 pr-4">{k.name}</td>
                  <td className="py-2 pr-4 font-mono text-xs">
                    {k.scopes.length === 0 ? t("noScopes") : k.scopes.join(", ")}
                  </td>
                  <td className="py-2 pr-4">{k.isActive ? t("active") : t("revoked")}</td>
                  <td className="py-2 pr-4 text-xs">
                    {k.lastUsedAt ? new Date(k.lastUsedAt).toLocaleString() : t("never")}
                  </td>
                  <td className="py-2 text-right">
                    {k.isActive && (
                      <button
                        type="button"
                        data-permission-public="true"
                        disabled={pendingKeyId === k.id}
                        onClick={() => handleRevoke(k.id)}
                        className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
                      >
                        {t("revoke")}
                      </button>
                    )}
                    {rowError?.keyId === k.id && (
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
