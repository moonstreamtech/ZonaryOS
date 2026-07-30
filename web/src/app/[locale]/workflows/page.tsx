// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations, setRequestLocale } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchDefinitions } from "@/lib/workflow";
import { Link } from "@/i18n/navigation";
import DefinitionBuilder from "@/components/Workflow/DefinitionBuilder";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Item 1: a firm-level "Workflows" view listing every workflow
// definition the firm has (internal/workflow.ListDefinitions), not one
// hardcoded card for stock_to_sale - a firm's second workflow
// definition, whenever one exists, shows up here automatically. Each
// entry links to /workflows/{key}, the generic instance view (see
// components/Workflow/WorkflowDefinitionView.tsx). Owners additionally
// get the "Define new workflow" builder (components/Workflow/DefinitionBuilder.tsx)
// - the frontend half of item 1's DefineWorkflowForFirm endpoint.
export default async function WorkflowsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Workflow");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const [definitions, role] = await Promise.all([
    fetchDefinitions(sessionToken, firm.firmId),
    fetchRoleInFirm(sessionToken, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-6 bg-zinc-50 px-6 py-16 dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("listTitle")}
      </h1>

      {role?.isOwner && (
        <div className="w-full max-w-md">
          <DefinitionBuilder firmId={firm.firmId} />
        </div>
      )}

      {definitions === null ? (
        <p className="text-red-600 dark:text-red-400">{t("listLoadError")}</p>
      ) : definitions.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("listEmpty")}</p>
      ) : (
        <ul className="flex w-full max-w-md flex-col gap-3">
          {definitions.map((definition) => (
            <li key={definition.definitionId}>
              <Link
                href={`/workflows/${definition.key}`}
                data-permission-public="true"
                className="block rounded-lg border border-zinc-200 bg-white px-4 py-3 text-black transition-colors hover:border-zinc-400 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-50 dark:hover:border-zinc-600"
              >
                {/* definition.name is workflow_definitions.name from the
                    backend - data, not UI copy. */}
                {definition.name}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </main>
  );
}
