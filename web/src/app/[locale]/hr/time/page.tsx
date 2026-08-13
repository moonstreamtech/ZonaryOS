// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchPeople } from "@/lib/hr";
import { fetchTimeEntries } from "@/lib/timetracking";
import TimeSheet from "@/components/TimeTracking/TimeSheet";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Time tracking batch: the firm's time sheet - log time entry form,
// filter by person and date range (internal/timetracking's own package
// doc comment: member-gated create/read, edit/delete restricted to the
// entry's own creator or an owner).
export default async function TimeTrackingPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("TimeTracking");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const entries = await fetchTimeEntries(sessionToken, firm.firmId);
  const people = (await fetchPeople(sessionToken, firm.firmId)) ?? [];

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
      </div>

      {entries === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <TimeSheet firmId={firm.firmId} entries={entries} people={people} />
      )}
    </main>
  );
}
