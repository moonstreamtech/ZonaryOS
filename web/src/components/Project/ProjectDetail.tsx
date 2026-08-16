// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import type { Project } from "@/lib/project";
import type { ProjectSummary } from "@/lib/project";
import type { Person } from "@/lib/hr";

type Props = {
  project: Project;
  summary: ProjectSummary | null;
  people: Person[];
};

function personName(people: Person[], id?: string): string {
  if (!id) return "—";
  return people.find((p) => p.id === id)?.fullName ?? id;
}

// The project detail page's body (this batch): plain info panel + the
// summary endpoint's data rendered directly (tasks-by-state as a simple
// completed/total progress bar plus a per-state breakdown, total hours
// logged, budget vs. estimated cost side by side when both are present -
// per the batch brief's explicit scoping, no linked-tasks list UI here).
// Server component - nothing on this page is interactive yet.
export default async function ProjectDetail({ project, summary, people }: Props) {
  const t = await getTranslations("Project");

  const doneCount = summary?.tasksByState.find((s) => s.stateKey === "done")?.count ?? 0;
  const progressPct = summary && summary.totalTasks > 0 ? Math.round((doneCount / summary.totalTasks) * 100) : 0;

  return (
    <div className="flex w-full max-w-3xl flex-col gap-8">
      <div className="flex flex-col gap-2 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
        <div className="flex items-center justify-between">
          <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{project.name}</h1>
          <span className="text-sm text-zinc-600 dark:text-zinc-400">{t(`status.${project.status}`)}</span>
        </div>
        {project.description && <p className="text-sm text-zinc-700 dark:text-zinc-300">{project.description}</p>}
        <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("startDate")}</dt>
            <dd className="text-black dark:text-zinc-50">{project.startDate ?? "—"}</dd>
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("endDate")}</dt>
            <dd className="text-black dark:text-zinc-50">{project.endDate ?? "—"}</dd>
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("manager")}</dt>
            <dd className="text-black dark:text-zinc-50">{personName(people, project.managerPersonId)}</dd>
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-400">{t("budget")}</dt>
            <dd className="text-black dark:text-zinc-50">
              {project.budget ? `${project.budget} ${project.currency}` : "—"}
            </dd>
          </div>
        </dl>
      </div>

      <div className="flex flex-col gap-3">
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("summaryTitle")}</h2>
        {summary === null ? (
          <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
        ) : (
          <div className="panel flex flex-col gap-4 p-4">
            <div className="flex flex-col gap-2">
              <div className="flex items-center justify-between text-sm">
                <span className="text-zinc-600 dark:text-zinc-400">{t("tasksProgress")}</span>
                <span className="font-mono text-black dark:text-zinc-50">
                  {doneCount} / {summary.totalTasks}
                </span>
              </div>
              <div className="h-2 w-full overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
                <div className="h-full bg-black dark:bg-zinc-50" style={{ width: `${progressPct}%` }} />
              </div>
            </div>

            <div className="flex flex-col gap-1">
              <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("tasksByStateTitle")}</h3>
              {summary.tasksByState.length === 0 ? (
                <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("noTasks")}</p>
              ) : (
                <ul className="flex flex-col gap-1 text-sm">
                  {summary.tasksByState.map((s) => (
                    <li key={s.stateKey} className="flex justify-between">
                      <span className="text-zinc-600 dark:text-zinc-400">{t(`taskState.${s.stateKey}`)}</span>
                      <span className="font-mono text-black dark:text-zinc-50">{s.count}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            <div className="flex justify-between text-sm">
              <span className="text-zinc-600 dark:text-zinc-400">{t("totalHoursLogged")}</span>
              <span className="font-mono text-black dark:text-zinc-50">{summary.totalHoursLogged}</span>
            </div>

            {(summary.budget || summary.estimatedCost) && (
              <div className="grid grid-cols-1 gap-3 border-t border-zinc-200 pt-3 sm:grid-cols-2 dark:border-zinc-800">
                <div>
                  <dt className="text-xs text-zinc-500 dark:text-zinc-400">{t("budget")}</dt>
                  <dd className="font-mono text-sm text-black dark:text-zinc-50">
                    {summary.budget ? `${summary.budget} ${project.currency}` : "—"}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-zinc-500 dark:text-zinc-400">{t("estimatedCost")}</dt>
                  <dd className="font-mono text-sm text-black dark:text-zinc-50">
                    {summary.estimatedCost ? `${summary.estimatedCost} ${project.currency}` : t("estimatedCostUnavailable")}
                  </dd>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
