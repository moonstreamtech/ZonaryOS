// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchContract, fetchContractDocuments } from "@/lib/contract";
import ContractDetail from "@/components/Contract/ContractDetail";

type PageProps = {
  params: Promise<{ locale: string; contractId: string }>;
};

// New internal/contracts package's contract detail page (this batch):
// full contract fields, its document list (owner-gated "add document"
// form), and a "Preview" link that opens the contract-render endpoint in
// a new tab. Member-gated read, owner-gated write - same tier
// /assets/[assetId] uses.
export default async function ContractDetailPage({ params }: PageProps) {
  const { locale, contractId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Contracts");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const contract = await fetchContract(sessionToken, firm.firmId, contractId);
  const documents = contract
    ? await fetchContractDocuments(sessionToken, firm.firmId, contractId)
    : null;

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {contract === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <ContractDetail
          firmId={firm.firmId}
          contract={contract}
          documents={documents ?? []}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
