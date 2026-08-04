"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Rule } from "@/lib/workflow";

type Props = {
  firmId: string;
  definitionKey: string;
  rules: Rule[];
  // isOwner gates the toggle-enabled/delete actions - a plain member can
  // still see this list (Vision §3's rule is visible in the UI even
  // without the full builder), but only an owner can mutate it, same
  // tier as defining a workflow/rule at all.
  isOwner: boolean;
};

// The read side of the Rule Engine Builder UI: every rule this workflow
// definition has, with an owner-only enable/disable toggle and delete
// button. Reuses WorkflowInstanceList.tsx's own list/row layout
// conventions rather than inventing a new one.
export default function RuleList({ firmId, definitionKey, rules, isOwner }: Props) {
  const t = useTranslations("Workflow");
  const router = useRouter();
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function toggleEnabled(rule: Rule) {
    setBusyId(rule.id);
    setError(null);
    try {
      const res = await fetch(`/api/workflow/rules/${encodeURIComponent(rule.id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ firmId, definitionKey, patch: { enabled: !rule.enabled } }),
      });
      if (!res.ok) {
        setError(t("ruleActionError"));
        return;
      }
      router.refresh();
    } catch {
      setError(t("ruleActionError"));
    } finally {
      setBusyId(null);
    }
  }

  async function remove(rule: Rule) {
    setBusyId(rule.id);
    setError(null);
    try {
      const res = await fetch(
        `/api/workflow/rules/${encodeURIComponent(rule.id)}?firmId=${encodeURIComponent(firmId)}&definitionKey=${encodeURIComponent(definitionKey)}`,
        { method: "DELETE" },
      );
      if (!res.ok) {
        setError(t("ruleActionError"));
        return;
      }
      router.refresh();
    } catch {
      setError(t("ruleActionError"));
    } finally {
      setBusyId(null);
    }
  }

  if (rules.length === 0) {
    return <p className="text-sm text-zinc-500 dark:text-zinc-500">{t("ruleListEmpty")}</p>;
  }

  return (
    <div className="flex w-full flex-col gap-2">
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
      <ul className="flex w-full flex-col gap-2">
        {rules.map((rule) => (
          <li
            key={rule.id}
            className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-zinc-200 bg-white px-4 py-3 dark:border-zinc-800 dark:bg-zinc-950"
          >
            <div className="flex flex-col gap-0.5">
              <span className="text-sm font-medium text-black dark:text-zinc-50">{rule.name}</span>
              <span className="text-xs text-zinc-500 dark:text-zinc-500">
                {rule.trigger === "on_create" ? t("ruleTriggerOnCreate") : t("ruleTriggerOnTransition")}
                {" · "}
                {rule.enabled ? t("ruleEnabledBadge") : t("ruleDisabledBadge")}
                {rule.autonomous ? ` · ${t("ruleAutonomousBadge")}` : ` · ${t("rulePendingApprovalBadge")}`}
              </span>
            </div>
            {isOwner && (
              <div className="flex gap-2">
                <button
                  type="button"
                  data-permission-public="true"
                  disabled={busyId === rule.id}
                  onClick={() => toggleEnabled(rule)}
                  className="rounded-md border border-zinc-300 px-2 py-1 text-xs text-zinc-600 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-400"
                >
                  {rule.enabled ? t("ruleDisableButton") : t("ruleEnableButton")}
                </button>
                <button
                  type="button"
                  data-permission-public="true"
                  disabled={busyId === rule.id}
                  onClick={() => remove(rule)}
                  className="rounded-md border border-zinc-300 px-2 py-1 text-xs text-zinc-600 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-400"
                >
                  {t("ruleDeleteButton")}
                </button>
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
