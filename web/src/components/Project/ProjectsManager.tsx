"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { Project, ProjectStatus } from "@/lib/project";
import type { Person } from "@/lib/hr";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  projects: Project[];
  people: Person[];
  isOwner: boolean;
};

function personName(people: Person[], id?: string): string {
  if (!id) return "—";
  return people.find((p) => p.id === id)?.fullName ?? id;
}

function statusBadgeClass(status: ProjectStatus): string {
  switch (status) {
    case "active":
      return "bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-400";
    case "on_hold":
      return "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-400";
    case "completed":
      return "bg-zinc-200 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300";
    case "cancelled":
      return "bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-400";
  }
}

// The /projects page's project list - mirrors BOMManager.tsx's own
// create-form/row shape: create is owner-gated (server-checked by
// internal/project's own CreateProject; isOwner here only controls what
// renders). Status is rendered as a small badge per the batch brief's
// "list page with status badges" - it's set on the project only via
// PATCH from the detail page, not from this list.
export default function ProjectsManager({ firmId, projects, people, isOwner }: Props) {
  const t = useTranslations("Project");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [budget, setBudget] = useState("");
  const [currency, setCurrency] = useState("");
  const [managerPersonId, setManagerPersonId] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim()) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/project/projects/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: name.trim(),
          description: description.trim() || undefined,
          startDate: startDate || undefined,
          endDate: endDate || undefined,
          budget: budget.trim() || undefined,
          currency: currency.trim() || undefined,
          managerPersonId: managerPersonId || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setName("");
      setDescription("");
      setStartDate("");
      setEndDate("");
      setBudget("");
      setCurrency("");
      setManagerPersonId("");
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
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("listTitle")}
          </h2>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newProject")}
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
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("projectName")}
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("description")}
              <input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
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
              {t("budget")}
              <input
                value={budget}
                onChange={(e) => setBudget(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("currency")}
              <input
                value={currency}
                onChange={(e) => setCurrency(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("manager")}
              <select
                value={managerPersonId}
                onChange={(e) => setManagerPersonId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("selectManager")}</option>
                {people.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.fullName}
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
            {t("create")}
          </button>
        </form>
      )}

      {projects.length === 0 ? (
        <EmptyState message={t("projectsEmpty")} />
      ) : (
        <div className="panel flex flex-col gap-3 p-0">
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnName")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnStatus")}</th>
                  <th className="py-2 pr-4 font-medium">{t("manager")}</th>
                  <th className="py-2 pr-4 font-medium">{t("budget")}</th>
                </tr>
              </thead>
              <tbody>
                {projects.map((p) => (
                  <tr
                    key={p.id}
                    className="border-b border-zinc-200 text-black last:border-0 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-50 dark:hover:bg-zinc-950"
                  >
                    <td className="py-2 pr-4 pl-4">
                      <Link
                        href={`/projects/${p.id}`}
                        data-permission-public="true"
                        className="underline-offset-4 hover:underline"
                      >
                        {p.name}
                      </Link>
                    </td>
                    <td className="py-2 pr-4">
                      <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusBadgeClass(p.status)}`}>
                        {t(`status.${p.status}`)}
                      </span>
                    </td>
                    <td className="py-2 pr-4">{personName(people, p.managerPersonId)}</td>
                    <td className="py-2 pr-4 font-mono">{p.budget ? `${p.budget} ${p.currency}` : "—"}</td>
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
