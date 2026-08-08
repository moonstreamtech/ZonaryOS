"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Contract, Person, PersonType } from "@/lib/hr";

type PersonWithContracts = Person & { contracts: Contract[] };

type Props = {
  firmId: string;
  people: PersonWithContracts[];
  isOwner: boolean;
};

const PERSON_TYPES: PersonType[] = ["employee", "contractor", "partner"];

// The HR page's people list with contract history - create/edit are
// owner-gated (server-checked by internal/hr's own CreatePerson/
// UpdatePerson/CreateContract; isOwner here only controls what renders,
// not what's allowed), mirroring components/Accounting/AccountsManager.tsx's
// own create-form/row-action shape.
export default function PeopleManager({ firmId, people, isOwner }: Props) {
  const t = useTranslations("HR");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [fullName, setFullName] = useState("");
  const [type, setType] = useState<PersonType>("employee");
  const [email, setEmail] = useState("");
  const [startDate, setStartDate] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [expandedPersonId, setExpandedPersonId] = useState<string | null>(null);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    const trimmedName = fullName.trim();
    if (!trimmedName) {
      setCreateError(t("createNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/hr/people/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          fullName: trimmedName,
          type,
          email: email || undefined,
          startDate: startDate || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setFullName("");
      setType("employee");
      setEmail("");
      setStartDate("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function toggleStatus(person: Person) {
    try {
      await fetch(`/api/hr/people/${firmId}/${person.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status: person.status === "active" ? "inactive" : "active" }),
      });
      router.refresh();
    } catch {
      // best-effort - the row simply won't reflect the change; a retry
      // is always available via the same button.
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      {isOwner && (
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("peopleTitle")}
          </h2>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newPerson")}
          </button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("fullName")}
              <input
                value={fullName}
                onChange={(e) => setFullName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("type")}
              <select
                value={type}
                onChange={(e) => setType(e.target.value as PersonType)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                {PERSON_TYPES.map((pt) => (
                  <option key={pt} value={pt}>
                    {t(`personType.${pt}`)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("email")}
              <input
                value={email}
                onChange={(e) => setEmail(e.target.value)}
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

      {people.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {people.map((person) => (
            <div key={person.id} className="rounded-md border border-zinc-300 dark:border-zinc-700">
              <button
                type="button"
                data-permission-public="true"
                onClick={() => setExpandedPersonId((cur) => (cur === person.id ? null : person.id))}
                className="flex w-full items-center justify-between gap-2 p-3 text-left text-sm"
              >
                <span className="font-medium text-black dark:text-zinc-50">{person.fullName}</span>
                <span className="text-xs text-zinc-500 dark:text-zinc-400">
                  {t(`personType.${person.type}`)} ·{" "}
                  {person.status === "active" ? t("statusActive") : t("statusInactive")}
                </span>
              </button>

              {expandedPersonId === person.id && (
                <div className="flex flex-col gap-3 border-t border-zinc-200 p-3 text-sm dark:border-zinc-800">
                  {isOwner && (
                    <button
                      type="button"
                      data-permission-public="true"
                      onClick={() => toggleStatus(person)}
                      className="self-start rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                    >
                      {person.status === "active" ? t("deactivate") : t("activate")}
                    </button>
                  )}

                  <h3 className="text-xs font-medium text-zinc-600 dark:text-zinc-400">
                    {t("contractsTitle")}
                  </h3>
                  {person.contracts.length === 0 ? (
                    <p className="text-xs text-zinc-500 dark:text-zinc-500">{t("noContracts")}</p>
                  ) : (
                    <ul className="flex flex-col gap-1">
                      {person.contracts.map((c) => (
                        <li
                          key={c.id}
                          className="flex items-center justify-between font-mono text-xs text-zinc-700 dark:text-zinc-300"
                        >
                          <span>
                            {c.type} · {c.startDate}
                            {c.endDate ? ` → ${c.endDate}` : ""}
                          </span>
                          <span>
                            {c.amount ? `${c.amount} ${c.currency}` : "—"}
                          </span>
                        </li>
                      ))}
                    </ul>
                  )}

                  {isOwner && <ContractForm firmId={firmId} personId={person.id} onCreated={() => router.refresh()} />}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ContractForm({
  firmId,
  personId,
  onCreated,
}: {
  firmId: string;
  personId: string;
  onCreated: () => void;
}) {
  const t = useTranslations("HR");
  const [type, setType] = useState("salary");
  const [amount, setAmount] = useState("");
  const [startDate, setStartDate] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!startDate) {
      setError(t("contractStartDateRequired"));
      return;
    }
    setSubmitting(true);
    try {
      const res = await fetch(`/api/hr/people/${firmId}/${personId}/contracts`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type, amount: amount || undefined, startDate }),
      });
      if (!res.ok) {
        setError(t("contractCreateError"));
        return;
      }
      setType("salary");
      setAmount("");
      setStartDate("");
      onCreated();
    } catch {
      setError(t("contractCreateError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form
      onSubmit={submit}
      data-permission-public="true"
      className="flex flex-wrap items-end gap-2 border-t border-zinc-200 pt-2 dark:border-zinc-800"
    >
      <label className="flex flex-col gap-1 text-xs">
        {t("contractType")}
        <input
          value={type}
          onChange={(e) => setType(e.target.value)}
          className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
        />
      </label>
      <label className="flex flex-col gap-1 text-xs">
        {t("contractAmount")}
        <input
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          placeholder="0.00"
          className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
        />
      </label>
      <label className="flex flex-col gap-1 text-xs">
        {t("contractStartDate")}
        <input
          type="date"
          value={startDate}
          onChange={(e) => setStartDate(e.target.value)}
          className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
        />
      </label>
      <button
        type="submit"
        data-permission-public="true"
        disabled={submitting}
        className="rounded-md bg-black px-2 py-1 text-xs font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
      >
        {t("addContract")}
      </button>
      {error && <p className="w-full text-xs text-red-600 dark:text-red-400">{error}</p>}
    </form>
  );
}
