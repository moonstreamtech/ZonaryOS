"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { InteractionType, Opportunity, TimelineEntry } from "@/lib/crm";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  customerId: string;
  timeline: TimelineEntry[] | null;
  opportunities: Opportunity[];
  isOwner: boolean;
};

const INTERACTION_TYPES: InteractionType[] = ["call", "email", "meeting", "note", "demo", "proposal"];

function timelineSummary(
  t: (key: string, values?: Record<string, string | number | Date>) => string,
  entry: TimelineEntry,
): string {
  if (entry.kind === "interaction" && entry.interaction) {
    return entry.interaction.summary;
  }
  if (entry.kind === "opportunity" && entry.opportunity) {
    return t("timelineOpportunitySummary", { name: entry.opportunity.name, stage: t(`opportunityStage.${entry.opportunity.stage}`) });
  }
  return "";
}

function toDatetimeLocalValue(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

// The customer detail page's Timeline section (this batch): the merged
// interactions+opportunities feed (internal/crm's own GetTimeline),
// newest-first as the backend already returns it, plus an owner-gated
// form to log a new interaction. Mirrors BOMManager.tsx's own
// create-form/list shape - a toggleable create panel above a simple list,
// same owner-gating (data-permission-public="true", server re-checked by
// internal/crm.CreateInteraction) rather than a shared Tabs primitive,
// since this codebase has none yet.
export default function CustomerTimeline({ firmId, customerId, timeline, opportunities, isOwner }: Props) {
  const t = useTranslations("CustomerDetail");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [type, setType] = useState<InteractionType>("call");
  const [date, setDate] = useState(() => toDatetimeLocalValue(new Date()));
  const [summary, setSummary] = useState("");
  const [outcome, setOutcome] = useState("");
  const [nextAction, setNextAction] = useState("");
  const [nextActionDate, setNextActionDate] = useState("");
  const [opportunityId, setOpportunityId] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!type || !date || !summary.trim()) {
      setCreateError(t("timelineCreateRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/crm/customers/${firmId}/${customerId}/interactions`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          type,
          date: new Date(date).toISOString(),
          summary: summary.trim(),
          outcome: outcome.trim() || undefined,
          nextAction: nextAction.trim() || undefined,
          // nextActionDate is a plain <input type="date"> value
          // (YYYY-MM-DD) - the backend parses it date-only
          // (crm.parseOptionalDate, time.Parse("2006-01-02", s)), unlike
          // `date` above (a real timestamptz column, hence toISOString()).
          nextActionDate: nextActionDate || undefined,
          opportunityId: opportunityId || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("timelineCreateError"));
        return;
      }
      setCreating(false);
      setType("call");
      setDate(toDatetimeLocalValue(new Date()));
      setSummary("");
      setOutcome("");
      setNextAction("");
      setNextActionDate("");
      setOpportunityId("");
      router.refresh();
    } catch {
      setCreateError(t("timelineCreateError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("timelineTitle")}</h2>
        {isOwner && (
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("timelineCancel") : t("timelineLogInteraction")}
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
              {t("timelineType")}
              <select
                value={type}
                onChange={(e) => setType(e.target.value as InteractionType)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {INTERACTION_TYPES.map((it) => (
                  <option key={it} value={it}>
                    {t(`interactionType.${it}`)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("timelineDate")}
              <input
                type="datetime-local"
                value={date}
                onChange={(e) => setDate(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("timelineSummary")}
              <input
                value={summary}
                onChange={(e) => setSummary(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("timelineOutcome")}
              <input
                value={outcome}
                onChange={(e) => setOutcome(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("timelineNextAction")}
              <input
                value={nextAction}
                onChange={(e) => setNextAction(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("timelineNextActionDate")}
              <input
                type="date"
                value={nextActionDate}
                onChange={(e) => setNextActionDate(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("timelineOpportunity")}
              <select
                value={opportunityId}
                onChange={(e) => setOpportunityId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("timelineNoOpportunity")}</option>
                {opportunities.map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.name}
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
            {t("timelineCreate")}
          </button>
        </form>
      )}

      {timeline === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : timeline.length === 0 ? (
        <EmptyState message={t("timelineEmpty")} />
      ) : (
        <ul className="flex flex-col gap-2">
          {timeline.map((entry, idx) => (
            <li
              key={idx}
              className="flex flex-col gap-1 rounded-md border border-zinc-300 p-3 text-sm dark:border-zinc-700"
            >
              <div className="flex items-center justify-between">
                <span className="font-medium text-black dark:text-zinc-50">
                  {t(`timelineKind.${entry.kind}`)}
                  {entry.kind === "interaction" && entry.interaction && (
                    <span className="ml-2 text-xs font-normal text-zinc-500 dark:text-zinc-400">
                      {t(`interactionType.${entry.interaction.type}`)}
                    </span>
                  )}
                </span>
                <span className="text-xs text-zinc-500 dark:text-zinc-400">
                  {new Date(entry.date).toLocaleString()}
                </span>
              </div>
              <p className="text-zinc-700 dark:text-zinc-300">{timelineSummary(t, entry)}</p>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
