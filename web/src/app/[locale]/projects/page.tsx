// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchProjects } from "@/lib/project";
import { fetchPeople } from "@/lib/hr";
import ProjectsManager from "@/components/Project/ProjectsManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// New internal/project package's list page (this batch): projects with
// status badges plus an owner-gated create form (manager picked from
// lib/hr.fetchPeople, same as every other person-picker in this
// codebase). Member-gated read, owner-gated write, same tier as
// /manufacturing/bom.
export default async function ProjectsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Project");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [projects, people] = await Promise.all([
    fetchProjects(sessionToken, firm.firmId),
    fetchPeople(sessionToken, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="w-full max-w-4xl">
        <ListPageHeader
          title={t("listTitle")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("listTitle") }]}
        />
      </div>

      {projects === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <ProjectsManager firmId={firm.firmId} projects={projects} people={people ?? []} isOwner={isOwner} />
      )}
    </main>
  );
}
