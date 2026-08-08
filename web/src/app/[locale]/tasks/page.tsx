// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchDefinitionByKey, fetchInstances, TASK_APPROVAL_KEY, type InstanceState } from "@/lib/workflow";
import { fetchPeople, type Person } from "@/lib/hr";

type PageProps = {
  params: Promise<{ locale: string }>;
  searchParams: Promise<{ status?: string; assignee?: string }>;
};

// The task/project management batch's first cross-module view: joins
// task_approval workflow instances (internal/workflow) with people
// (internal/hr) on the instance payload's assignee_person_id field
// (FieldTypePerson, see internal/workflow/task_approval.go) - both
// fetched independently (there is no single backend query spanning both
// tables; RLS/authorization for each is still checked by its own
// package), then joined here in the page itself. This is architecturally
// the first place two previously-separate modules' data are combined
// into one view; it's still just two ordinary, independently-authorized
// reads, not a new privileged read path.
export default async function TasksPage({ params, searchParams }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Tasks");
  const { status: statusFilter, assignee: assigneeFilter } = await searchParams;

  const { sessionToken, firm } = await requireFirmContext(locale);

  const definition = await fetchDefinitionByKey(sessionToken, firm.firmId, TASK_APPROVAL_KEY);
  if (!definition) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-4 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
        <h1 className="text-2xl font-semibold text-black dark:text-zinc-50">{t("title")}</h1>
        <p className="text-zinc-600 dark:text-zinc-400">{t("definitionMissing")}</p>
      </main>
    );
  }

  const [instances, people] = await Promise.all([
    fetchInstances(sessionToken, firm.firmId, definition.definitionId),
    fetchPeople(sessionToken, firm.firmId),
  ]);

  const peopleById = new Map<string, Person>((people ?? []).map((p) => [p.id, p]));

  let filtered: InstanceState[] = instances ?? [];
  if (statusFilter) {
    filtered = filtered.filter((inst) => inst.state.key === statusFilter);
  }
  if (assigneeFilter) {
    filtered = filtered.filter((inst) => inst.payload.assignee_person_id === assigneeFilter);
  }

  const statusOptions = ["open", "in_progress", "done", "rejected"];

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-3xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
      </div>

      <form method="get" className="flex w-full max-w-3xl flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("filterStatus")}
          <select
            name="status"
            defaultValue={statusFilter ?? ""}
            className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          >
            <option value="">{t("filterAll")}</option>
            {statusOptions.map((s) => (
              <option key={s} value={s}>
                {t(`status.${s}`)}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("filterAssignee")}
          <select
            name="assignee"
            defaultValue={assigneeFilter ?? ""}
            className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          >
            <option value="">{t("filterAll")}</option>
            {(people ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.fullName}
              </option>
            ))}
          </select>
        </label>
        <button
          type="submit"
          className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white dark:bg-zinc-50 dark:text-black"
        >
          {t("applyButton")}
        </button>
      </form>

      {instances === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : filtered.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="w-full max-w-3xl overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("columnState")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnAssignee")}</th>
                <th className="py-2 font-medium">{t("columnPayload")}</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((inst) => {
                const assigneeId = inst.payload.assignee_person_id;
                const assignee =
                  typeof assigneeId === "string" ? peopleById.get(assigneeId) : undefined;
                return (
                  <tr
                    key={inst.instanceId}
                    className="border-b border-zinc-200 align-top text-black dark:border-zinc-800 dark:text-zinc-50"
                  >
                    <td className="py-2 pr-4">{inst.state.name}</td>
                    <td className="py-2 pr-4">{assignee ? assignee.fullName : t("unassigned")}</td>
                    <td className="py-2 font-mono text-xs text-zinc-600 dark:text-zinc-400">
                      {JSON.stringify(inst.payload)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </main>
  );
}
