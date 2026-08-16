// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchContracts } from "@/lib/contract";
import ContractsManager from "@/components/Contract/ContractsManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// New internal/contracts package's contract registry list page (this
// batch): a general-purpose contract list with a status filter and
// expiry highlighting, plus an owner-gated create form. Member-gated
// read, owner-gated write, same tier as /assets.
export default async function ContractsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Contracts");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const contracts = await fetchContracts(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="w-full max-w-4xl">
        <ListPageHeader
          title={t("listTitle")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("listTitle") }]}
        />
      </div>

      {contracts === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <ContractsManager firmId={firm.firmId} contracts={contracts} isOwner={isOwner} />
      )}
    </main>
  );
}
