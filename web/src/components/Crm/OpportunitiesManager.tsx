"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useMemo, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Customer } from "@/lib/crm";
import type { Opportunity, OpportunityStage } from "@/lib/crm";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  opportunities: Opportunity[];
  customers: Customer[];
  isOwner: boolean;
};

const STAGES: OpportunityStage[] = ["prospect", "qualified", "proposal", "negotiation", "won", "lost"];
const VIEW_STORAGE_KEY = "zonaryos.crm.opportunitiesView";

function customerName(customers: Customer[], id: string): string {
  return customers.find((c) => c.id === id)?.name ?? id;
}

function sumValue(opportunities: Opportunity[]): number {
  return opportunities.reduce((sum, o) => sum + (o.value ? Number(o.value) || 0 : 0), 0);
}

// The /crm/opportunities page's pipeline manager (this batch): a
// kanban/list toggle (CSS-only columns, explicitly no drag-and-drop
// library per the batch brief) persisted client-side in localStorage,
// per-stage pipeline value totals computed from the already-fetched list
// (no new endpoint), and an owner-gated create form. Mirrors
// BOMManager.tsx's own create-form shape; stage changes are a plain
// dropdown per card/row that calls the stage-update endpoint - the
// server (internal/crm.UpdateOpportunityStage) is the actual authority
// on valid transitions (won/lost are terminal), this component only
// offers every stage and lets a rejected change surface inline.
export default function OpportunitiesManager({ firmId, opportunities, customers, isOwner }: Props) {
  const t = useTranslations("Crm");
  const router = useRouter();

  // Read the persisted view preference lazily (initializer function, not
  // an effect) - avoids a synchronous setState-in-effect render, at the
  // cost of "kanban" being the very first server-rendered frame before
  // hydration picks up whatever was in localStorage (acceptable for a
  // client-only display preference, same tradeoff noted in this file's
  // own header comment on why this is a localStorage choice, not a DB
  // column).
  const [view, setView] = useState<"kanban" | "list">(() => {
    if (typeof window === "undefined") return "kanban";
    const stored = window.localStorage.getItem(VIEW_STORAGE_KEY);
    return stored === "list" ? "list" : "kanban";
  });

  function toggleView(next: "kanban" | "list") {
    setView(next);
    window.localStorage.setItem(VIEW_STORAGE_KEY, next);
  }

  const totalsByStage = useMemo(() => {
    const map = new Map<OpportunityStage, number>();
    for (const stage of STAGES) {
      map.set(stage, sumValue(opportunities.filter((o) => o.stage === stage)));
    }
    return map;
  }, [opportunities]);

  const [creating, setCreating] = useState(false);
  const [customerId, setCustomerId] = useState("");
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [probability, setProbability] = useState("");
  const [expectedCloseDate, setExpectedCloseDate] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!customerId || !name.trim()) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/crm/opportunities/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          customerId,
          name: name.trim(),
          value: value.trim() || undefined,
          probability: probability.trim() ? Number(probability.trim()) : undefined,
          // expectedCloseDate is already YYYY-MM-DD from the <input
          // type="date"> below - the backend parses it with
          // time.Parse("2006-01-02", s) (crm.parseOptionalDate), so it
          // must be passed through as-is, not re-serialized via
          // toISOString() (which would produce a full RFC3339 timestamp
          // the backend's date-only parser rejects).
          expectedCloseDate: expectedCloseDate || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setCustomerId("");
      setName("");
      setValue("");
      setProbability("");
      setExpectedCloseDate("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  const [busyId, setBusyId] = useState<string | null>(null);
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});

  async function changeStage(opportunityId: string, stage: OpportunityStage) {
    setRowErrors((prev) => ({ ...prev, [opportunityId]: "" }));
    setBusyId(opportunityId);
    try {
      const res = await fetch(`/api/crm/opportunities/${firmId}/${opportunityId}/stage`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ stage }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { error?: string };
        setRowErrors((prev) => ({ ...prev, [opportunityId]: body.error || t("stageChangeError") }));
        return;
      }
      router.refresh();
    } catch {
      setRowErrors((prev) => ({ ...prev, [opportunityId]: t("stageChangeError") }));
    } finally {
      setBusyId(null);
    }
  }

  function StageSelect({ opportunity }: { opportunity: Opportunity }) {
    const terminal = opportunity.stage === "won" || opportunity.stage === "lost";
    return (
      <select
        value={opportunity.stage}
        disabled={terminal || busyId === opportunity.id}
        onChange={(e) => changeStage(opportunity.id, e.target.value as OpportunityStage)}
        data-permission-public="true"
        className="rounded-md border border-zinc-300 px-2 py-1 text-xs dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
      >
        {STAGES.map((s) => (
          <option key={s} value={s}>
            {t(`stage.${s}`)}
          </option>
        ))}
      </select>
    );
  }

  return (
    <div className="flex w-full max-w-5xl flex-col gap-6">
      {/* Pipeline value per stage */}
      <div className="grid w-full grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        {STAGES.map((stage) => (
          <div
            key={stage}
            className="flex flex-col gap-1 rounded-md border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-950"
          >
            <span className="text-xs font-medium text-zinc-500 dark:text-zinc-400">{t(`stage.${stage}`)}</span>
            <span className="text-sm font-semibold text-black dark:text-zinc-50">
              {totalsByStage.get(stage)?.toLocaleString() ?? 0}
            </span>
          </div>
        ))}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <button
            type="button"
            data-permission-public="true"
            onClick={() => toggleView("kanban")}
            className={
              view === "kanban"
                ? "rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white dark:bg-zinc-50 dark:text-black"
                : "rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            }
          >
            {t("viewKanban")}
          </button>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => toggleView("list")}
            className={
              view === "list"
                ? "rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white dark:bg-zinc-50 dark:text-black"
                : "rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            }
          >
            {t("viewList")}
          </button>
        </div>
        {isOwner && (
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newOpportunity")}
          </button>
        )}
      </div>

      {isOwner && creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("customer")}
              <select
                value={customerId}
                onChange={(e) => setCustomerId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("selectCustomer")}</option>
                {customers.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("opportunityName")}
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("value")}
              <input
                value={value}
                onChange={(e) => setValue(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("probability")}
              <input
                value={probability}
                onChange={(e) => setProbability(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("expectedCloseDate")}
              <input
                type="date"
                value={expectedCloseDate}
                onChange={(e) => setExpectedCloseDate(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
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

      {opportunities.length === 0 ? (
        <EmptyState message={t("opportunitiesEmpty")} />
      ) : view === "kanban" ? (
        <div className="grid w-full grid-cols-1 gap-3 overflow-x-auto sm:grid-cols-2 lg:grid-cols-6">
          {STAGES.map((stage) => {
            const stageOpportunities = opportunities.filter((o) => o.stage === stage);
            return (
              <div key={stage} className="flex min-w-0 flex-col gap-2 rounded-md border border-zinc-200 bg-zinc-50 p-2 dark:border-zinc-800 dark:bg-zinc-950">
                <h3 className="px-1 text-xs font-medium text-zinc-500 dark:text-zinc-400">
                  {t(`stage.${stage}`)} ({stageOpportunities.length})
                </h3>
                <div className="flex flex-col gap-2">
                  {stageOpportunities.map((o) => (
                    <div key={o.id} className="flex flex-col gap-2 rounded-md border border-zinc-200 bg-white p-2 text-sm dark:border-zinc-800 dark:bg-black">
                      <span className="font-medium text-black dark:text-zinc-50">{o.name}</span>
                      <span className="text-xs text-zinc-500 dark:text-zinc-400">{customerName(customers, o.customerId)}</span>
                      {o.value && <span className="font-mono text-xs">{o.value} {o.currency}</span>}
                      {isOwner && <StageSelect opportunity={o} />}
                      {rowErrors[o.id] && <p className="text-xs text-red-600 dark:text-red-400">{rowErrors[o.id]}</p>}
                    </div>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      ) : (
        <div className="panel flex flex-col gap-3 p-0">
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnName")}</th>
                  <th className="py-2 pr-4 font-medium">{t("customer")}</th>
                  <th className="py-2 pr-4 font-medium">{t("value")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnStage")}</th>
                  {isOwner && <th className="py-2 pr-4 font-medium">{t("actionsTitle")}</th>}
                </tr>
              </thead>
              <tbody>
                {opportunities.map((o) => (
                  <tr key={o.id} className="border-b border-zinc-200 text-black last:border-0 dark:border-zinc-800 dark:text-zinc-50">
                    <td className="py-2 pr-4 pl-4">{o.name}</td>
                    <td className="py-2 pr-4">{customerName(customers, o.customerId)}</td>
                    <td className="py-2 pr-4 font-mono">{o.value ? `${o.value} ${o.currency}` : "—"}</td>
                    <td className="py-2 pr-4">{t(`stage.${o.stage}`)}</td>
                    {isOwner && (
                      <td className="py-2 pr-4">
                        <div className="flex flex-col gap-1">
                          <StageSelect opportunity={o} />
                          {rowErrors[o.id] && <p className="text-xs text-red-600 dark:text-red-400">{rowErrors[o.id]}</p>}
                        </div>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
