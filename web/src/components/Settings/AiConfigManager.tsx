"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { AIConfiguration, AIProvider } from "@/lib/aiConfig";

type Props = {
  firmId: string;
  configs: AIConfiguration[];
};

const PROVIDERS: AIProvider[] = ["anthropic", "openai", "google", "custom"];

// The /settings/ai-config page's client half: a create form (provider
// dropdown, model, API key) and a per-row delete action, mirroring
// components/Settings/ApiKeysManager.tsx's own create-form/table shape.
// Creating a new configuration activates it and deactivates every other
// one for the firm (internal/ai.CreateConfig's own "single active
// configuration per firm" contract) - this component doesn't model that
// itself, it just re-fetches (router.refresh()) after every mutation so
// the isActive column always reflects the server's own state.
//
// Owner-gated: this page only ever renders for an owner (the server
// component checks role.isOwner before mounting this at all), tagged
// data-permission-public rather than data-permission-key - same
// convention as ApiKeysManager.tsx's own doc comment, since
// CreateConfig/DeleteConfig are gated by the structural is_owner check
// (internal/permission.IsOwner), not a permission-catalog entry.
export default function AiConfigManager({ firmId, configs }: Props) {
  const t = useTranslations("AiConfigSettings");
  const router = useRouter();

  const [provider, setProvider] = useState<AIProvider>("anthropic");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [pendingConfigId, setPendingConfigId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ configId: string; message: string } | null>(null);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!model.trim() || !apiKey.trim()) {
      setCreateError(t("createFieldsRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/ai-config/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ provider, model: model.trim(), apiKey: apiKey.trim() }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setModel("");
      setApiKey("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDelete(configId: string) {
    setRowError(null);
    setPendingConfigId(configId);
    try {
      const res = await fetch(`/api/ai-config/${firmId}/${configId}`, { method: "DELETE" });
      if (!res.ok) {
        setRowError({ configId, message: t("deleteError") });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ configId, message: t("deleteError") });
    } finally {
      setPendingConfigId(null);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <form
        onSubmit={submitCreate}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newConfigTitle")}</h2>
        <p className="text-xs text-zinc-500 dark:text-zinc-400">{t("newConfigHint")}</p>

        <label className="flex flex-col gap-1 text-sm">
          {t("provider")}
          <select
            value={provider}
            onChange={(e) => setProvider(e.target.value as AIProvider)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          >
            {PROVIDERS.map((p) => (
              <option key={p} value={p}>
                {t(`providerName.${p}`)}
              </option>
            ))}
          </select>
        </label>

        <label className="flex flex-col gap-1 text-sm">
          {t("model")}
          <input
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder={t("modelPlaceholder")}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>

        <label className="flex flex-col gap-1 text-sm">
          {t("apiKey")}
          <input
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            autoComplete="off"
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
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

      {configs.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("provider")}</th>
                <th className="py-2 pr-4 font-medium">{t("model")}</th>
                <th className="py-2 pr-4 font-medium">{t("apiKey")}</th>
                <th className="py-2 pr-4 font-medium">{t("status")}</th>
                <th className="py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {configs.map((c) => (
                <tr key={c.id} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
                  <td className="py-2 pr-4">{t(`providerName.${c.provider}`)}</td>
                  <td className="py-2 pr-4 font-mono text-xs">{c.model}</td>
                  <td className="py-2 pr-4 font-mono text-xs">{c.apiKeyMasked}</td>
                  <td className="py-2 pr-4">{c.isActive ? t("active") : t("inactive")}</td>
                  <td className="py-2 text-right">
                    <button
                      type="button"
                      data-permission-public="true"
                      disabled={pendingConfigId === c.id}
                      onClick={() => handleDelete(c.id)}
                      className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
                    >
                      {t("delete")}
                    </button>
                    {rowError?.configId === c.id && (
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
