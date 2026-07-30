// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import {
  fetchDefinitionByKey,
  fetchInstances,
} from "@/lib/workflow";
import { fetchAuditLog } from "@/lib/auditlog";
import CreateInstanceForm from "./CreateInstanceForm";
import WorkflowInstanceList from "./WorkflowInstanceList";
import WorkflowHistory from "./WorkflowHistory";

type Props = {
  sessionToken: string;
  firmId: string;
  workflowKey: string;
};

// The one generic page body every workflow definition's page renders -
// resolve the definition by key, list its instances, offer the generic
// create-instance form, show its history - shared by both the /stock
// route (a thin wrapper around this component fixed to
// STOCK_TO_SALE_KEY, kept working at its existing URL) and the new
// /workflows/[key] route (any other definition the firm has). Adding a
// firm's second workflow definition needs zero new frontend code: this
// component, its GenericInstanceList/CreateInstanceForm/WorkflowHistory
// children, and the backend's own AvailableAction.Name/PermissionKey are
// the whole story - see internal/workflow/workflow_integration_test.go's
// purchaseOrderSpec for the proof this isn't just true by coincidence
// for stock_to_sale.
export default async function WorkflowDefinitionView({
  sessionToken,
  firmId,
  workflowKey,
}: Props) {
  const t = await getTranslations("Workflow");

  const definition = await fetchDefinitionByKey(sessionToken, firmId, workflowKey);
  if (!definition) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-4 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
        <h1 className="text-2xl font-semibold text-black dark:text-zinc-50">
          {t("title")}
        </h1>
        <p className="text-red-600 dark:text-red-400">{t("definitionMissing")}</p>
      </main>
    );
  }

  const instances =
    (await fetchInstances(sessionToken, firmId, definition.definitionId)) ?? [];

  // Sale/transaction history reuses the audit log's own data rather than
  // a parallel history mechanism. Not every member can read it:
  // internal/auditlog.List is gated by audit.log.read, held by the owner
  // role by default - a null result here just means the section is
  // omitted below, not an error.
  const auditEntries = await fetchAuditLog(sessionToken, firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-2xl flex-col items-center gap-6">
        {/* definition.name is workflow_definitions.name from the
            backend - data, not UI copy, same convention as workflow
            state/action names elsewhere in this component tree. */}
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {definition.name}
        </h1>

        <CreateInstanceForm
          firmId={firmId}
          definitionId={definition.definitionId}
          createPermissionKey={definition.createPermissionKey}
        />

        <WorkflowInstanceList firmId={firmId} instances={instances} />
      </div>

      {auditEntries && (
        <div className="w-full max-w-2xl">
          <WorkflowHistory instances={instances} entries={auditEntries} />
        </div>
      )}
    </main>
  );
}
