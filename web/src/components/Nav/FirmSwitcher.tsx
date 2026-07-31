"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { MeFirm } from "@/lib/me";

type Props = {
  firms: MeFirm[];
  activeFirmId: string;
};

// Vision §3's many-firms-per-user model: lets a member of more than one
// firm switch which one the rest of the app (stock, audit log, the
// dashboard) is scoped to. Renders nothing but the firm's own name when
// there's only one membership - the common case doesn't need a dropdown.
// Switching writes app/api/firm/switch's cookie, then refreshes the
// current route so every server component re-resolves against the new
// active firm.
export default function FirmSwitcher({ firms, activeFirmId }: Props) {
  const t = useTranslations("Nav");
  const router = useRouter();
  const [pending, setPending] = useState(false);

  if (firms.length <= 1) {
    return (
      <span className="text-sm font-medium text-black dark:text-zinc-50">
        {firms[0]?.firmName}
      </span>
    );
  }

  async function switchFirm(firmId: string) {
    if (firmId === activeFirmId) return;
    setPending(true);
    try {
      const res = await fetch("/api/firm/switch", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ firmId }),
      });
      if (res.ok) {
        router.refresh();
      }
    } finally {
      setPending(false);
    }
  }

  return (
    <label className="flex items-center gap-2 text-sm">
      <span className="sr-only">{t("firmSwitcherLabel")}</span>
      <select
        aria-label={t("firmSwitcherLabel")}
        data-permission-public="true"
        value={activeFirmId}
        disabled={pending}
        onChange={(e) => switchFirm(e.target.value)}
        className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black disabled:opacity-50 dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
      >
        {firms.map((firm) => (
          <option key={firm.firmId} value={firm.firmId}>
            {firm.firmName}
          </option>
        ))}
      </select>
    </label>
  );
}
