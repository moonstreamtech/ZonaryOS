"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { EdgeAgent } from "@/lib/edgeAgents";

type Props = {
  firmId: string;
  agents: EdgeAgent[];
  isOwner: boolean;
};

const STATUS_DOT: Record<EdgeAgent["status"], string> = {
  offline: "bg-zinc-400",
  online: "bg-green-500",
  error: "bg-red-500",
};

// The /edge-agents page's agent list - registering a new agent is
// owner-gated (server-checked by internal/edgeagent's own CreateAgent;
// isOwner here only controls what renders), mirroring
// components/Logistics/DeliveriesManager.tsx's own create-form/row shape.
// Each row links through to that agent's own detail page, where token
// management and the event log live.
export default function EdgeAgentsManager({ firmId, agents, isOwner }: Props) {
  const t = useTranslations("EdgeAgents");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    const trimmedName = name.trim();
    if (!trimmedName) {
      setCreateError(t("createNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/edge-agents/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: trimmedName, description: description || undefined }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setName("");
      setDescription("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("agentsTitle")}</h2>
        {isOwner && (
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newAgent")}
          </button>
        )}
      </div>

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <label className="flex flex-col gap-1 text-sm">
            {t("name")}
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("description")}
            <input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
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
      )}

      {agents.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {agents.map((a) => (
            <li key={a.id}>
              <Link
                href={`/edge-agents/${a.id}`}
                data-permission-public="true"
                className="flex items-center justify-between rounded-md border border-zinc-300 px-4 py-3 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-900"
              >
                <span className="flex items-center gap-2">
                  <span className={`h-2 w-2 rounded-full ${STATUS_DOT[a.status]}`} aria-hidden="true" />
                  <span className="font-medium text-black dark:text-zinc-50">{a.name}</span>
                </span>
                <span className="flex items-center gap-4 text-zinc-600 dark:text-zinc-400">
                  <span>{t(`status.${a.status}`)}</span>
                  <span>{a.lastSeenAt ? new Date(a.lastSeenAt).toLocaleString() : t("neverSeen")}</span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
