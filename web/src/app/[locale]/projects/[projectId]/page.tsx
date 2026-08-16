// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchProject, fetchProjectSummary } from "@/lib/project";
import { fetchPeople } from "@/lib/hr";
import ProjectDetail from "@/components/Project/ProjectDetail";

type PageProps = {
  params: Promise<{ locale: string; projectId: string }>;
};

// New internal/project package's detail page (this batch): project info
// plus its task/time/budget summary (internal/project's own GetSummary),
// rendered directly - no linked-tasks list UI, out of scope for this
// batch per the brief. Member-gated, same tier as the list page.
export default async function ProjectDetailPage({ params }: PageProps) {
  const { locale, projectId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Project");

  const { sessionToken, firm } = await requireFirmContext(locale);

  const [project, summary, people] = await Promise.all([
    fetchProject(sessionToken, firm.firmId, projectId),
    fetchProjectSummary(sessionToken, firm.firmId, projectId),
    fetchPeople(sessionToken, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {project === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <ProjectDetail project={project} summary={summary} people={people ?? []} />
      )}
    </main>
  );
}
