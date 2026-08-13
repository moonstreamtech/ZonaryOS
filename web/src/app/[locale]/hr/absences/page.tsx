// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchPeople } from "@/lib/hr";
import { fetchAbsences } from "@/lib/absence";
import AbsencesManager from "@/components/Absences/AbsencesManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// HR depth batch: absence list, request form, approve/reject buttons for
// owners (permission-tagged) - see internal/absence's own package doc
// comment (approval is backed by a real workflow.absence_approval
// instance, not a bespoke mechanism).
export default async function AbsencesPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Absences");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const absences = await fetchAbsences(sessionToken, firm.firmId);
  const people = (await fetchPeople(sessionToken, firm.firmId)) ?? [];

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
      </div>

      {absences === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <AbsencesManager firmId={firm.firmId} absences={absences} people={people} isOwner={isOwner} />
      )}
    </main>
  );
}
