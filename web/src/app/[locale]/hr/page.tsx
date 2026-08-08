// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchPeople, fetchContracts } from "@/lib/hr";
import PeopleManager from "@/components/HR/PeopleManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Vision §3's minimal HR foundation: people list with contract summary.
// Member-gated read, owner-gated write (see internal/hr's own package
// doc comment) - this page renders for any member, but PeopleManager
// only shows create/edit controls when isOwner (the backend enforces
// this regardless; the frontend gate is just so a non-owner isn't shown
// controls that would 403).
export default async function HRPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("HR");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const people = await fetchPeople(sessionToken, firm.firmId);

  // Contract history is fetched eagerly, per person, server-side - HR
  // rosters are small (dozens, not thousands) for any firm this batch's
  // scope targets, so this stays simple rather than adding a second
  // client-side fetch + GET proxy route just to lazy-load contracts on
  // expand.
  const peopleWithContracts =
    people !== null
      ? await Promise.all(
          people.map(async (p) => ({
            ...p,
            contracts: (await fetchContracts(sessionToken, firm.firmId, p.id)) ?? [],
          })),
        )
      : null;

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
      </div>

      {peopleWithContracts === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <PeopleManager firmId={firm.firmId} people={peopleWithContracts} isOwner={isOwner} />
      )}
    </main>
  );
}
