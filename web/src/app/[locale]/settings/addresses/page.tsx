// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchAddresses } from "@/lib/localization";
import AddressesManager from "@/components/Settings/AddressesManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Address and tax framework foundation (this batch, internal/
// localization) - firm-scoped address book. Owner-gated the same way
// settings/accounts/page.tsx is (isOwner, not a permission key - see
// that page's own doc comment for why "authorized" means is_owner for
// firm-structural settings pages in this codebase).
export default async function AddressesSettingsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("AddressesSettings");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);

  if (!role?.isOwner) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-4 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
        <h1 className="text-2xl font-semibold text-black dark:text-zinc-50">{t("title")}</h1>
        <p className="text-red-600 dark:text-red-400">{t("notAuthorized")}</p>
      </main>
    );
  }

  const addresses = await fetchAddresses(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-3xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
        <p className="max-w-2xl text-center text-sm text-zinc-600 dark:text-zinc-400">{t("description")}</p>
      </div>

      {addresses === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <AddressesManager firmId={firm.firmId} addresses={addresses} />
      )}
    </main>
  );
}
