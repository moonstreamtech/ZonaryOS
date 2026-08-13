"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useEffect, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Person } from "@/lib/hr";
import type { Entry, Period } from "@/lib/payroll";

type Props = {
  firmId: string;
  periods: Period[];
  people: Person[];
  isOwner: boolean;
};

// The payroll page's period list - create/close are owner-gated
// (server-checked by internal/payroll's own CreatePeriod/ClosePeriod;
// isOwner here only controls what renders, not what's allowed), mirroring
// components/HR/PeopleManager.tsx's own create-form/row-action shape.
export default function PayrollManager({ firmId, periods, people, isOwner }: Props) {
  const t = useTranslations("Payroll");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [periodStart, setPeriodStart] = useState("");
  const [periodEnd, setPeriodEnd] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [expandedPeriodId, setExpandedPeriodId] = useState<string | null>(null);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim() || !periodStart || !periodEnd) {
      setCreateError(t("createFieldsRequired"));
      return;
    }
    setSubmitting(true);
    try {
      const res = await fetch(`/api/payroll/periods/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name.trim(), periodStart, periodEnd }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setName("");
      setPeriodStart("");
      setPeriodEnd("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      {isOwner && (
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("periodsTitle")}</h2>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newPeriod")}
          </button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <label className="flex flex-col gap-1 text-sm">
              {t("periodName")}
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("periodStart")}
              <input
                type="date"
                value={periodStart}
                onChange={(e) => setPeriodStart(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("periodEnd")}
              <input
                type="date"
                value={periodEnd}
                onChange={(e) => setPeriodEnd(e.target.value)}
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

      {periods.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {periods.map((period) => (
            <div key={period.id} className="rounded-md border border-zinc-300 dark:border-zinc-700">
              <button
                type="button"
                data-permission-public="true"
                onClick={() => setExpandedPeriodId((cur) => (cur === period.id ? null : period.id))}
                className="flex w-full items-center justify-between gap-2 p-3 text-left text-sm"
              >
                <span className="font-medium text-black dark:text-zinc-50">
                  {period.name} · {period.periodStart} → {period.periodEnd}
                </span>
                <span className="text-xs text-zinc-500 dark:text-zinc-400">{t(`status.${period.status}`)}</span>
              </button>

              {expandedPeriodId === period.id && (
                <PeriodDetail
                  firmId={firmId}
                  period={period}
                  people={people}
                  isOwner={isOwner}
                  onChanged={() => router.refresh()}
                />
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function PeriodDetail({
  firmId,
  period,
  people,
  isOwner,
  onChanged,
}: {
  firmId: string;
  period: Period;
  people: Person[];
  isOwner: boolean;
  onChanged: () => void;
}) {
  const t = useTranslations("Payroll");
  const [entries, setEntries] = useState<Entry[] | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [closing, setClosing] = useState(false);
  const [closeError, setCloseError] = useState<string | null>(null);

  useEffect(() => {
    fetch(`/api/payroll/periods/${firmId}/${period.id}/entries`)
      .then((res) => (res.ok ? res.json() : Promise.reject()))
      .then((data: Entry[]) => setEntries(data))
      .catch(() => setLoadError(true));
  }, [firmId, period.id]);

  async function submitClose() {
    setCloseError(null);
    setClosing(true);
    try {
      const res = await fetch(`/api/payroll/periods/${firmId}/${period.id}/close`, { method: "POST" });
      if (!res.ok) {
        setCloseError(t("closeError"));
        return;
      }
      onChanged();
    } catch {
      setCloseError(t("closeError"));
    } finally {
      setClosing(false);
    }
  }

  const personName = (id: string) => people.find((p) => p.id === id)?.fullName ?? id;

  return (
    <div className="flex flex-col gap-3 border-t border-zinc-200 p-3 text-sm dark:border-zinc-800">
      {isOwner && period.status !== "closed" && (
        <button
          type="button"
          data-permission-public="true"
          onClick={submitClose}
          disabled={closing}
          className="self-start rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
        >
          {t("closePeriod")}
        </button>
      )}
      {closeError && <p className="text-xs text-red-600 dark:text-red-400">{closeError}</p>}

      <h3 className="text-xs font-medium text-zinc-600 dark:text-zinc-400">{t("entriesTitle")}</h3>
      {loadError ? (
        <p className="text-xs text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : entries === null ? (
        <p className="text-xs text-zinc-500 dark:text-zinc-500">{t("loadingEntries")}</p>
      ) : entries.length === 0 ? (
        <p className="text-xs text-zinc-500 dark:text-zinc-500">{t("noEntries")}</p>
      ) : (
        <ul className="flex flex-col gap-1">
          {entries.map((entry) => (
            <li key={entry.id} className="flex items-center justify-between font-mono text-xs text-zinc-700 dark:text-zinc-300">
              <span>{personName(entry.personId)}</span>
              <span>
                {entry.grossAmount} → {entry.netAmount} {entry.currency}
              </span>
            </li>
          ))}
        </ul>
      )}

      {isOwner && period.status === "open" && (
        <EntryForm firmId={firmId} periodId={period.id} people={people} onCreated={onChanged} />
      )}
    </div>
  );
}

function EntryForm({
  firmId,
  periodId,
  people,
  onCreated,
}: {
  firmId: string;
  periodId: string;
  people: Person[];
  onCreated: () => void;
}) {
  const t = useTranslations("Payroll");
  const [personId, setPersonId] = useState(people[0]?.id ?? "");
  const [gross, setGross] = useState("");
  const [net, setNet] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!personId || !gross || !net) {
      setError(t("entryFieldsRequired"));
      return;
    }
    setSubmitting(true);
    try {
      const res = await fetch(`/api/payroll/periods/${firmId}/${periodId}/entries`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ personId, grossAmount: gross, netAmount: net }),
      });
      if (!res.ok) {
        setError(t("entryCreateError"));
        return;
      }
      setGross("");
      setNet("");
      onCreated();
    } catch {
      setError(t("entryCreateError"));
    } finally {
      setSubmitting(false);
    }
  }

  if (people.length === 0) return null;

  return (
    <form onSubmit={submit} data-permission-public="true" className="flex flex-wrap items-end gap-2 border-t border-zinc-200 pt-2 dark:border-zinc-800">
      <label className="flex flex-col gap-1 text-xs">
        {t("entryPerson")}
        <select
          value={personId}
          onChange={(e) => setPersonId(e.target.value)}
          className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
        >
          {people.map((p) => (
            <option key={p.id} value={p.id}>
              {p.fullName}
            </option>
          ))}
        </select>
      </label>
      <label className="flex flex-col gap-1 text-xs">
        {t("entryGross")}
        <input value={gross} onChange={(e) => setGross(e.target.value)} placeholder="0.00" className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
      </label>
      <label className="flex flex-col gap-1 text-xs">
        {t("entryNet")}
        <input value={net} onChange={(e) => setNet(e.target.value)} placeholder="0.00" className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
      </label>
      <button
        type="submit"
        data-permission-public="true"
        disabled={submitting}
        className="rounded-md bg-black px-2 py-1 text-xs font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
      >
        {t("addEntry")}
      </button>
      {error && <p className="w-full text-xs text-red-600 dark:text-red-400">{error}</p>}
    </form>
  );
}
