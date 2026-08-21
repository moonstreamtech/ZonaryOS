"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { OnboardingProgress } from "@/lib/onboarding";
import Panel from "@/components/ui/Panel";

type Props = {
  firmId: string;
  progress: OnboardingProgress;
};

// Fixed step -> destination route mapping, mirroring
// internal/onboarding.Steps' own fixed order - each step's "go do this"
// link, not the backend's concern at all (the backend only ever sees the
// step key string).
const STEP_ROUTES: Record<string, string> = {
  create_first_workflow: "/workflows",
  add_first_product: "/inventory",
  invite_team_member: "/settings/members",
  run_first_report: "/reports",
  configure_webhook: "/settings/webhooks",
};

// First-run onboarding checklist widget (Part 1 of the onboarding/help/
// UX batch): a collapsible, dismissible progress card on the dashboard.
// Step completion itself is entirely server-driven (see
// internal/onboarding.CompleteStep's own doc comment) - this component
// only ever reads progress and offers the Dismiss action; there is no
// "mark complete" button anywhere in this UI, by design.
export default function OnboardingChecklist({ firmId, progress }: Props) {
  const t = useTranslations("Onboarding");
  const router = useRouter();
  const [collapsed, setCollapsed] = useState(false);
  const [dismissing, setDismissing] = useState(false);
  const [dismissed, setDismissed] = useState(Boolean(progress.dismissedAt));

  const total = progress.steps.length;
  const completedCount = progress.steps.filter((s) => progress.completedSteps.includes(s)).length;
  const allDone = total > 0 && completedCount === total;

  if (dismissed || allDone) {
    return null;
  }

  async function dismiss() {
    setDismissing(true);
    try {
      const res = await fetch(`/api/onboarding/${firmId}`, { method: "PATCH" });
      if (res.ok) {
        setDismissed(true);
        router.refresh();
      }
    } finally {
      setDismissing(false);
    }
  }

  return (
    <Panel className="!bg-zinc-50 dark:!bg-zinc-950">
      <div className="flex items-center justify-between gap-3">
        <div className="flex flex-1 flex-col gap-1">
          <h2 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">{t("title")}</h2>
          <div className="flex items-center gap-2">
            <div className="h-1.5 w-40 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
              <div
                className="h-full bg-black dark:bg-zinc-50"
                style={{ width: `${total === 0 ? 0 : (completedCount / total) * 100}%` }}
              />
            </div>
            <span className="text-xs text-zinc-500 dark:text-zinc-500">
              {t("progressLabel", { completed: completedCount, total })}
            </span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCollapsed((c) => !c)}
            className="text-xs font-medium text-zinc-600 underline dark:text-zinc-400"
          >
            {collapsed ? t("expand") : t("collapse")}
          </button>
          <button
            type="button"
            data-permission-public="true"
            disabled={dismissing}
            onClick={dismiss}
            className="text-xs font-medium text-zinc-600 underline dark:text-zinc-400 disabled:opacity-50"
          >
            {t("dismiss")}
          </button>
        </div>
      </div>

      {!collapsed && (
        <ul className="mt-3 flex flex-col gap-2">
          {progress.steps.map((step) => {
            const done = progress.completedSteps.includes(step);
            return (
              <li key={step} className="flex items-center gap-3">
                <span
                  className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full border text-xs ${
                    done
                      ? "border-green-600 bg-green-600 text-white dark:border-green-500 dark:bg-green-500"
                      : "border-zinc-300 text-transparent dark:border-zinc-700"
                  }`}
                  aria-hidden="true"
                >
                  ✓
                </span>
                <span className={done ? "text-sm text-zinc-500 line-through dark:text-zinc-500" : "text-sm text-zinc-800 dark:text-zinc-200"}>
                  {t(`step.${step}`)}
                </span>
                {!done && STEP_ROUTES[step] && (
                  <Link
                    href={STEP_ROUTES[step]}
                    data-permission-public="true"
                    className="ml-auto text-xs font-medium text-zinc-950 underline dark:text-zinc-50"
                  >
                    {t("goTo")}
                  </Link>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}
