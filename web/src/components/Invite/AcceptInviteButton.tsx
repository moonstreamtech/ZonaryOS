"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";

type Props = {
  locale: string;
  token: string;
};

// The one client-side piece of /invite/[token]: an authenticated visitor
// clicking this POSTs /api/invite-accept/[token] (proxying the Go
// backend's POST /api/invites/{token}/accept). On failure this calls
// router.refresh() instead of rendering its own error copy - the page's
// server component re-runs lookupInvite and shows whichever specific
// message (already used/expired/revoked) now applies, so there is exactly
// one place in this codebase that decides what each invite state's
// message says, not two that could drift apart.
export default function AcceptInviteButton({ locale, token }: Props) {
  const t = useTranslations("Invite");
  const router = useRouter();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState(false);

  async function handleAccept() {
    setSubmitting(true);
    setError(false);
    try {
      const res = await fetch(`/api/invite-accept/${token}`, { method: "POST" });
      if (!res.ok) {
        setError(true);
        router.refresh();
        return;
      }
      router.push(`/${locale}`);
      router.refresh();
    } catch {
      setError(true);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex flex-col items-center gap-2">
      <button
        type="button"
        disabled={submitting}
        onClick={handleAccept}
        className="rounded-full bg-foreground px-5 py-2 text-sm font-medium text-background transition-colors hover:bg-[#383838] disabled:opacity-50 dark:hover:bg-[#ccc]"
      >
        {submitting ? t("accepting") : t("acceptButton")}
      </button>
      {error && <p className="text-sm text-red-600 dark:text-red-400">{t("acceptError")}</p>}
    </div>
  );
}
