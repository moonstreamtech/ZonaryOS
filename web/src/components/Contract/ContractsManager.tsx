"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useMemo, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { Contract, ContractType, ContractStatus, CounterpartyType } from "@/lib/contract";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  contracts: Contract[];
  isOwner: boolean;
};

const CONTRACT_TYPES: ContractType[] = ["customer", "supplier", "employment", "nda", "service", "other"];
const CONTRACT_STATUSES: ContractStatus[] = ["draft", "review", "active", "expired", "terminated", "renewed"];
const COUNTERPARTY_TYPES: CounterpartyType[] = ["customer", "supplier", "person", "other"];

function statusBadgeClass(status: ContractStatus): string {
  switch (status) {
    case "active":
      return "bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-400";
    case "review":
      return "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-400";
    case "draft":
      return "bg-zinc-200 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300";
    case "expired":
    case "terminated":
      return "bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-400";
    case "renewed":
      return "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-400";
  }
}

// Expiry highlighting: only active, non-auto-renewing contracts with an
// endDate are flagged - the same predicate
// internal/contracts.ListExpiringContracts (and the background expiry
// scheduler) use server-side. This is a purely visual client-side
// re-derivation of that predicate against `now`, not a second source of
// truth for which contracts are "expiring" (the /contracts/expiring
// endpoint remains the authority for that).
function expiryLevel(contract: Contract): "red" | "amber" | null {
  if (contract.status !== "active" || contract.autoRenewal || !contract.endDate) return null;
  const end = new Date(`${contract.endDate}T00:00:00Z`).getTime();
  if (Number.isNaN(end)) return null;
  const daysLeft = (end - Date.now()) / (1000 * 60 * 60 * 24);
  if (daysLeft < 7) return "red";
  if (daysLeft < 30) return "amber";
  return null;
}

function expiryRowClass(level: "red" | "amber" | null): string {
  switch (level) {
    case "red":
      return "bg-red-50 dark:bg-red-950/40";
    case "amber":
      return "bg-amber-50 dark:bg-amber-950/40";
    default:
      return "";
  }
}

// The /contracts page's contract list - mirrors AssetsManager.tsx's own
// create-form/row/status-badge shape: create is owner-gated
// (server-checked by internal/contracts.CreateContract; isOwner here
// only controls what renders). Date fields use <input type="date"> and
// send the raw "YYYY-MM-DD" .value string, never .toISOString() - the
// backend's startDate/endDate are DATE columns, not timestamps.
export default function ContractsManager({ firmId, contracts, isOwner }: Props) {
  const t = useTranslations("Contracts");
  const router = useRouter();

  const [statusFilter, setStatusFilter] = useState<ContractStatus | "all">("all");

  const filtered = useMemo(
    () => (statusFilter === "all" ? contracts : contracts.filter((c) => c.status === statusFilter)),
    [contracts, statusFilter],
  );

  const [creating, setCreating] = useState(false);
  const [referenceNumber, setReferenceNumber] = useState("");
  const [title, setTitle] = useState("");
  const [type, setType] = useState<ContractType>("customer");
  const [counterpartyName, setCounterpartyName] = useState("");
  const [counterpartyType, setCounterpartyType] = useState<CounterpartyType | "">("");
  const [status, setStatus] = useState<ContractStatus>("draft");
  const [value, setValue] = useState("");
  const [currency, setCurrency] = useState("");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [autoRenewal, setAutoRenewal] = useState(false);
  const [renewalNoticeDays, setRenewalNoticeDays] = useState("");
  const [notes, setNotes] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!referenceNumber.trim() || !title.trim() || !counterpartyName.trim()) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/contracts/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          referenceNumber: referenceNumber.trim(),
          title: title.trim(),
          type,
          counterpartyName: counterpartyName.trim(),
          counterpartyType: counterpartyType || undefined,
          status,
          value: value.trim() || undefined,
          currency: currency.trim() || undefined,
          startDate: startDate || undefined,
          endDate: endDate || undefined,
          autoRenewal,
          renewalNoticeDays: renewalNoticeDays ? Number(renewalNoticeDays) : undefined,
          notes: notes.trim() || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setReferenceNumber("");
      setTitle("");
      setType("customer");
      setCounterpartyName("");
      setCounterpartyType("");
      setStatus("draft");
      setValue("");
      setCurrency("");
      setStartDate("");
      setEndDate("");
      setAutoRenewal(false);
      setRenewalNoticeDays("");
      setNotes("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className="flex items-center gap-2 text-sm">
          {t("filterByStatus")}
          <select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value as ContractStatus | "all")}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          >
            <option value="all">{t("allStatuses")}</option>
            {CONTRACT_STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(`statuses.${s}`)}
              </option>
            ))}
          </select>
        </label>
        {isOwner && (
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newContract")}
          </button>
        )}
      </div>

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("referenceNumber")}
              <input
                value={referenceNumber}
                onChange={(e) => setReferenceNumber(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("title")}
              <input
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("type")}
              <select
                value={type}
                onChange={(e) => setType(e.target.value as ContractType)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {CONTRACT_TYPES.map((ct) => (
                  <option key={ct} value={ct}>
                    {t(`types.${ct}`)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("counterpartyName")}
              <input
                value={counterpartyName}
                onChange={(e) => setCounterpartyName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("counterpartyType")}
              <select
                value={counterpartyType}
                onChange={(e) => setCounterpartyType(e.target.value as CounterpartyType | "")}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("counterpartyTypeUnset")}</option>
                {COUNTERPARTY_TYPES.map((ct) => (
                  <option key={ct} value={ct}>
                    {t(`counterpartyTypes.${ct}`)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("status")}
              <select
                value={status}
                onChange={(e) => setStatus(e.target.value as ContractStatus)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {CONTRACT_STATUSES.map((s) => (
                  <option key={s} value={s}>
                    {t(`statuses.${s}`)}
                  </option>
                ))}
              </select>
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
              {t("currency")}
              <input
                value={currency}
                onChange={(e) => setCurrency(e.target.value)}
                placeholder="TRY" // i18n-ignore: an ISO 4217 currency code example, not translatable copy
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("startDate")}
              <input
                type="date"
                value={startDate}
                onChange={(e) => setStartDate(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("endDate")}
              <input
                type="date"
                value={endDate}
                onChange={(e) => setEndDate(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("renewalNoticeDays")}
              <input
                type="number"
                min={0}
                value={renewalNoticeDays}
                onChange={(e) => setRenewalNoticeDays(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={autoRenewal}
                onChange={(e) => setAutoRenewal(e.target.checked)}
                className="h-4 w-4 rounded border-zinc-300 dark:border-zinc-700"
              />
              {t("autoRenewal")}
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("notes")}
              <input
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
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

      {filtered.length === 0 ? (
        <EmptyState message={t("contractsEmpty")} />
      ) : (
        <div className="panel flex flex-col gap-3 p-0">
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnReferenceNumber")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnTitle")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnCounterparty")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnType")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnStatus")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnEndDate")}</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((c) => {
                  const level = expiryLevel(c);
                  return (
                    <tr
                      key={c.id}
                      className={`border-b border-zinc-200 text-black last:border-0 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-50 dark:hover:bg-zinc-950 ${expiryRowClass(level)}`}
                    >
                      <td className="py-2 pr-4 pl-4">
                        <Link
                          href={`/contracts/${c.id}`}
                          data-permission-public="true"
                          className="underline-offset-4 hover:underline"
                        >
                          {c.referenceNumber}
                        </Link>
                      </td>
                      <td className="py-2 pr-4">{c.title}</td>
                      <td className="py-2 pr-4">{c.counterpartyName}</td>
                      <td className="py-2 pr-4">{t(`types.${c.type}`)}</td>
                      <td className="py-2 pr-4">
                        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusBadgeClass(c.status)}`}>
                          {t(`statuses.${c.status}`)}
                        </span>
                      </td>
                      <td className="py-2 pr-4">
                        {c.endDate ?? "—"}
                        {level === "red" && (
                          <span className="ml-2 rounded-full bg-red-100 px-2 py-0.5 text-xs font-medium text-red-800 dark:bg-red-950 dark:text-red-400">
                            {t("expirySoon")}
                          </span>
                        )}
                        {level === "amber" && (
                          <span className="ml-2 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-950 dark:text-amber-400">
                            {t("expiryUpcoming")}
                          </span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
