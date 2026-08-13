"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useMemo, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Person } from "@/lib/hr";
import type { TimeEntry } from "@/lib/timetracking";

type Props = {
  firmId: string;
  entries: TimeEntry[];
  people: Person[];
};

// The time tracking page's sheet view + log form + person/date filter -
// any member can log time (internal/timetracking's own package doc
// comment); edit/delete restriction to the entry's own creator (or an
// owner) is enforced server-side, this page only shows the log/view/
// filter surface, not per-row edit controls.
export default function TimeSheet({ firmId, entries, people }: Props) {
  const t = useTranslations("TimeTracking");
  const router = useRouter();

  const [filterPerson, setFilterPerson] = useState("");
  const [filterFrom, setFilterFrom] = useState("");
  const [filterTo, setFilterTo] = useState("");

  const [logging, setLogging] = useState(false);
  const [personId, setPersonId] = useState(people[0]?.id ?? "");
  const [date, setDate] = useState("");
  const [hours, setHours] = useState("");
  const [description, setDescription] = useState("");
  const [billable, setBillable] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const personName = (id: string) => people.find((p) => p.id === id)?.fullName ?? id;

  const filtered = useMemo(() => {
    return entries.filter((e) => {
      if (filterPerson && e.personId !== filterPerson) return false;
      if (filterFrom && e.date < filterFrom) return false;
      if (filterTo && e.date > filterTo) return false;
      return true;
    });
  }, [entries, filterPerson, filterFrom, filterTo]);

  async function submitLog(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!personId || !date || !hours) {
      setError(t("logFieldsRequired"));
      return;
    }
    setSubmitting(true);
    try {
      const res = await fetch(`/api/timetracking/entries/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ personId, date, hours, description: description || undefined, billable }),
      });
      if (!res.ok) {
        setError(t("logError"));
        return;
      }
      setDate("");
      setHours("");
      setDescription("");
      setBillable(false);
      setLogging(false);
      router.refresh();
    } catch {
      setError(t("logError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex flex-col gap-1 text-xs">
            {t("filterPerson")}
            <select
              value={filterPerson}
              onChange={(e) => setFilterPerson(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            >
              <option value="">{t("filterAllPeople")}</option>
              {people.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.fullName}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs">
            {t("filterFrom")}
            <input type="date" value={filterFrom} onChange={(e) => setFilterFrom(e.target.value)} className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
          </label>
          <label className="flex flex-col gap-1 text-xs">
            {t("filterTo")}
            <input type="date" value={filterTo} onChange={(e) => setFilterTo(e.target.value)} className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
          </label>
        </div>
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setLogging((v) => !v)}
          className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
        >
          {logging ? t("cancel") : t("logTime")}
        </button>
      </div>

      {logging && (
        <form onSubmit={submitLog} data-permission-public="true" className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("entryPerson")}
              <select value={personId} onChange={(e) => setPersonId(e.target.value)} className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50">
                {people.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.fullName}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("entryDate")}
              <input type="date" value={date} onChange={(e) => setDate(e.target.value)} className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("entryHours")}
              <input value={hours} onChange={(e) => setHours(e.target.value)} placeholder="8" className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("entryDescription")}
              <input value={description} onChange={(e) => setDescription(e.target.value)} className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={billable} onChange={(e) => setBillable(e.target.checked)} />
              {t("entryBillable")}
            </label>
          </div>
          {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
          <button
            type="submit"
            data-permission-public="true"
            disabled={submitting}
            className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
          >
            {t("logTime")}
          </button>
        </form>
      )}

      {filtered.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="overflow-x-auto rounded-md border border-zinc-300 dark:border-zinc-700">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-200 text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
                <th className="p-2">{t("colPerson")}</th>
                <th className="p-2">{t("colDate")}</th>
                <th className="p-2">{t("colHours")}</th>
                <th className="p-2">{t("colDescription")}</th>
                <th className="p-2">{t("colBillable")}</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((entry) => (
                <tr key={entry.id} className="border-b border-zinc-100 last:border-0 dark:border-zinc-900">
                  <td className="p-2">{personName(entry.personId)}</td>
                  <td className="p-2 font-mono">{entry.date}</td>
                  <td className="p-2 font-mono">{entry.hours}</td>
                  <td className="p-2 text-zinc-600 dark:text-zinc-400">{entry.description ?? "—"}</td>
                  <td className="p-2">{entry.billable ? t("yes") : t("no")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
