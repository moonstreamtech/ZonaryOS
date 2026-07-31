"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useTranslations } from "next-intl";
import type { WorkflowDefinition } from "@/lib/workflow";
import CreateInstanceForm from "./CreateInstanceForm";

type Props = {
  firmId: string;
  definitions: WorkflowDefinition[];
};

// Item 4: a lightweight way to create an instance of any workflow
// definition directly from the dashboard - a picker plus the *exact
// same* CreateInstanceForm every /workflows/[key] page already uses,
// not a duplicate/simplified create form. This component only decides
// *which* definition's form to show; CreateInstanceForm still owns the
// whole "build a payload, POST it, surface 403/network errors" story.
export default function QuickCreatePanel({ firmId, definitions }: Props) {
  const t = useTranslations("Dashboard");
  const [open, setOpen] = useState(false);
  const [definitionId, setDefinitionId] = useState(
    definitions[0]?.definitionId ?? "",
  );

  if (definitions.length === 0) {
    return (
      <p className="text-sm text-zinc-500 dark:text-zinc-500">
        {t("quickCreateEmpty")}
      </p>
    );
  }

  const selected = definitions.find((d) => d.definitionId === definitionId);

  return (
    <div className="flex w-full max-w-2xl flex-col gap-3">
      <button
        type="button"
        data-permission-public="true"
        onClick={() => setOpen((prev) => !prev)}
        className="self-start rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
      >
        {open ? t("quickCreateClose") : t("quickCreateButton")}
      </button>

      {open && (
        <div className="flex flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950">
          <div className="flex flex-col gap-1">
            <label
              htmlFor="quick-create-definition"
              className="text-xs text-zinc-600 dark:text-zinc-400"
            >
              {t("quickCreateSelectLabel")}
            </label>
            <select
              id="quick-create-definition"
              value={definitionId}
              data-permission-public="true"
              onChange={(e) => setDefinitionId(e.target.value)}
              className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
            >
              {definitions.map((d) => (
                // d.name is workflow_definitions.name from the backend -
                // data, not UI copy, same convention as elsewhere.
                <option key={d.definitionId} value={d.definitionId}>
                  {d.name}
                </option>
              ))}
            </select>
          </div>

          {selected && (
            <CreateInstanceForm
              key={selected.definitionId}
              firmId={firmId}
              definitionId={selected.definitionId}
              createPermissionKey={selected.createPermissionKey}
              fields={selected.fields}
            />
          )}
        </div>
      )}
    </div>
  );
}
