"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Person } from "@/lib/hr";
import type { Absence, AbsenceType } from "@/lib/absence";

type Props = {
  firmId: string;
  absences: Absence[];
  people: Person[];
  isOwner: boolean;
};

const ABSENCE_TYPES: AbsenceType[] = ["vacation", "sick", "unpaid", "maternity", "other"];

// The absences page's list + request form + owner approve/reject actions.
// Requesting is member-gated (any member can request their own absence -
// internal/absence.CreateAbsence's own doc comment); approve/reject are
// owner-gated (server-checked by internal/absence.Approve/Reject -
// isOwner here only controls what renders).
export default function AbsencesManager({ firmId, absences, people, isOwner }: Props) {
  const t = useTranslations("Absences");
  const router = useRouter();

  const [requesting, setRequesting] = useState(false);
  const [personId, setPersonId] = useState(people[0]?.id ?? "");
  const [type, setType] = useState<AbsenceType>("vacation");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [notes, setNotes] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actingOn, setActingOn] = useState<string | null>(null);

  const personName = (id: string) => people.find((p) => p.id === id)?.fullName ?? id;

  async function submitRequest(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!personId || !startDate || !endDate) {
      setError(t("requestFieldsRequired"));
      return;
    }
    setSubmitting(true);
    try {
      const res = await fetch(`/api/absences/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ personId, type, startDate, endDate, notes: notes || undefined }),
      });
      if (!res.ok) {
        setError(t("requestError"));
        return;
      }
      setStartDate("");
      setEndDate("");
      setNotes("");
      setRequesting(false);
      router.refresh();
    } catch {
      setError(t("requestError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function act(absenceId: string, action: "approve" | "reject") {
    setActionError(null);
    setActingOn(absenceId);
    try {
      const res = await fetch(`/api/absences/${firmId}/${absenceId}/${action}`, { method: "PATCH" });
      if (!res.ok) {
        setActionError(t("actionError"));
        return;
      }
      router.refresh();
    } catch {
      setActionError(t("actionError"));
    } finally {
      setActingOn(null);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("listTitle")}</h2>
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setRequesting((v) => !v)}
          className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
        >
          {requesting ? t("cancel") : t("newRequest")}
        </button>
      </div>

      {requesting && (
        <form onSubmit={submitRequest} data-permission-public="true" className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("requestPerson")}
              <select value={personId} onChange={(e) => setPersonId(e.target.value)} className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50">
                {people.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.fullName}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("requestType")}
              <select value={type} onChange={(e) => setType(e.target.value as AbsenceType)} className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50">
                {ABSENCE_TYPES.map((at) => (
                  <option key={at} value={at}>
                    {t(`type.${at}`)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("requestStartDate")}
              <input type="date" value={startDate} onChange={(e) => setStartDate(e.target.value)} className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("requestEndDate")}
              <input type="date" value={endDate} onChange={(e) => setEndDate(e.target.value)} className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("requestNotes")}
              <input value={notes} onChange={(e) => setNotes(e.target.value)} className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50" />
            </label>
          </div>
          {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
          <button
            type="submit"
            data-permission-public="true"
            disabled={submitting}
            className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
          >
            {t("submitRequest")}
          </button>
        </form>
      )}

      {actionError && <p className="text-sm text-red-600 dark:text-red-400">{actionError}</p>}

      {absences.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {absences.map((abs) => (
            <div key={abs.id} className="flex flex-col gap-2 rounded-md border border-zinc-300 p-3 text-sm dark:border-zinc-700">
              <div className="flex items-center justify-between">
                <span className="font-medium text-black dark:text-zinc-50">
                  {personName(abs.personId)} · {t(`type.${abs.type}`)}
                </span>
                <span className="text-xs text-zinc-500 dark:text-zinc-400">{t(`status.${abs.status}`)}</span>
              </div>
              <span className="font-mono text-xs text-zinc-600 dark:text-zinc-400">
                {abs.startDate} → {abs.endDate}
              </span>
              {isOwner && abs.status === "pending" && (
                <div className="flex gap-2">
                  <button
                    type="button"
                    data-permission="workflow.absence_approval.approve"
                    onClick={() => act(abs.id, "approve")}
                    disabled={actingOn === abs.id}
                    className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                  >
                    {t("approve")}
                  </button>
                  <button
                    type="button"
                    data-permission="workflow.absence_approval.reject"
                    onClick={() => act(abs.id, "reject")}
                    disabled={actingOn === abs.id}
                    className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                  >
                    {t("reject")}
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
