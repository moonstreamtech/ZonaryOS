"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { PendingApproval } from "@/lib/approvals";

type Props = {
  firmId: string;
  approvals: PendingApproval[];
};

// The /approvals page's list: every pending approval this caller could
// act on (already filtered server-side by internal/workflow.ListPendingApprovals),
// each with an Approve/Reject pair. Approve executes the proposed
// transition for real (internal/workflow.Approve); Reject marks it
// resolved and notifies the requester - both server-checked again against
// the caller's real permission, this component's buttons are not the
// authorization boundary.
export default function ApprovalsManager({ firmId, approvals }: Props) {
  const t = useTranslations("Approvals");
  const router = useRouter();
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function resolve(id: string, action: "approve" | "reject") {
    setBusyId(id);
    setError(null);
    try {
      const res = await fetch(`/api/approvals/${firmId}/${id}/${action}`, { method: "POST" });
      if (!res.ok) {
        setError(t("actionError"));
        return;
      }
      router.refresh();
    } catch {
      setError(t("actionError"));
    } finally {
      setBusyId(null);
    }
  }

  if (approvals.length === 0) {
    return <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>;
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-4">
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
      <ul className="flex flex-col gap-3">
        {approvals.map((a) => (
          <li
            key={a.id}
            className="flex flex-col gap-2 rounded-md border border-zinc-300 p-4 text-sm dark:border-zinc-700"
          >
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="font-medium text-black dark:text-zinc-50">{a.proposedActionKey}</span>
              {a.expiresAt && (
                <span className="text-xs text-zinc-500 dark:text-zinc-500">
                  {t("expires")}: {new Date(a.expiresAt).toLocaleString()}
                </span>
              )}
            </div>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-zinc-600 dark:text-zinc-400">
              <div>
                <dt className="inline">{t("requestedBy")}: </dt>
                <dd className="inline">{a.requestedBy}</dd>
              </div>
              <div>
                <dt className="inline">{t("proposedAction")}: </dt>
                <dd className="inline">{a.proposedActionKey}</dd>
              </div>
            </dl>
            <div className="flex gap-2">
              <button
                type="button"
                data-permission-public="true"
                disabled={busyId === a.id}
                onClick={() => resolve(a.id, "approve")}
                className="rounded-md bg-black px-3 py-1.5 text-xs font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
              >
                {t("approve")}
              </button>
              <button
                type="button"
                data-permission-public="true"
                disabled={busyId === a.id}
                onClick={() => resolve(a.id, "reject")}
                className="rounded-md border border-zinc-300 px-3 py-1.5 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
              >
                {t("reject")}
              </button>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
