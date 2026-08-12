// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import {
  fetchDefinitionByKey,
  fetchDefinitions,
  fetchInstances,
  fetchInstancesPage,
  fetchRules,
  type AvailableAction,
} from "@/lib/workflow";
import { fetchAuditLog } from "@/lib/auditlog";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchPeople } from "@/lib/hr";
import { fetchProducts, fetchSuppliers } from "@/lib/inventory";
import { fetchDeliveries } from "@/lib/logistics";
import { fetchCustomers } from "@/lib/crm";
import CreateInstanceForm from "./CreateInstanceForm";
import WorkflowInstanceList from "./WorkflowInstanceList";
import WorkflowHistory from "./WorkflowHistory";
import RuleBuilder from "./RuleBuilder";
import RuleList from "./RuleList";
import ListPageHeader from "@/components/ui/ListPageHeader";

// Page size for the instance list below - not user-configurable (no UI
// for it), just the fixed page size pagination pages through.
const INSTANCES_PAGE_SIZE = 20;

type Props = {
  sessionToken: string;
  firmId: string;
  workflowKey: string;
  // From the page's own `searchParams` (Next.js's URL query param prop
  // for a server component) - `page`/`q` drive server-side paging and
  // search, not client state, so the URL stays the source of truth
  // (shareable/bookmarkable, survives a refresh).
  page?: number;
  q?: string;
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
  page = 1,
  q = "",
}: Props) {
  const t = await getTranslations("Workflow");
  const tNav = await getTranslations("Nav");

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

  const safePage = Math.max(1, page);
  const instancesPage = await fetchInstancesPage(
    sessionToken,
    firmId,
    definition.definitionId,
    { limit: INSTANCES_PAGE_SIZE, offset: (safePage - 1) * INSTANCES_PAGE_SIZE, q },
  );

  // Sale/transaction history reuses the audit log's own data rather than
  // a parallel history mechanism. Not every member can read it:
  // internal/auditlog.List is gated by audit.log.read, held by the owner
  // role by default - a null result here just means the section is
  // omitted below, not an error.
  const auditEntries = await fetchAuditLog(sessionToken, firmId);

  // WorkflowHistory's own payload fallback (see history.ts) wants every
  // instance's current payload, not just the current page's - a
  // separate, unpaginated fetch, same as before pagination existed. Only
  // fetched when there's actually a history section to render (i.e. the
  // caller can read the audit log at all), so a listing-only caller
  // doesn't pay for a second, unpaginated query on top of the paged one
  // above.
  const allInstances = auditEntries
    ? ((await fetchInstances(sessionToken, firmId, definition.definitionId)) ?? [])
    : [];

  // Rule Engine Builder UI (Open Points item 7's frontend batch): the
  // rule list is visible to any member (ListRulesForDefinition is only
  // membership-checked, see rules.go), but creating/editing/deleting one
  // is owner-only, same tier as DefinitionBuilder's own workflow-creation
  // gate. RuleBuilder's "state" leaf definitionKey dropdown and
  // "transition" action's actionKey dropdown both need real data the
  // definition alone doesn't carry: existingDefinitionKeys (the firm's
  // OTHER definitions, same list DefinitionBuilder's own reference-field
  // picker already uses) and availableActions (every distinct action key
  // reachable from the currently-instantiated states - the closest
  // available proxy for "every transition this definition has," since no
  // endpoint returns the full state graph independent of any instance).
  // people is only fetched when this definition actually has a "person"-
  // typed field (e.g. task_approval's assignee_person_id) - CreateInstanceForm's
  // person picker (a plain <select>, see its own doc comment) needs the
  // real roster; every other definition skips this fetch entirely.
  const needsPeople = (definition.fields ?? []).some((f) => f.type === "person");
  // needsProducts/needsSuppliers (Inventory management batch) are the
  // exact same "only fetch the catalog a definition's own schema actually
  // needs" pattern needsPeople's own doc comment establishes above -
  // CreateInstanceForm's product/supplier pickers (a plain <select>, same
  // shape as its person picker) need the real catalogs.
  const needsProducts = (definition.fields ?? []).some((f) => f.type === "product");
  const needsSuppliers = (definition.fields ?? []).some((f) => f.type === "supplier");
  // needsDeliveries/needsCustomers (Logistics/CRM batch) are the exact
  // same pattern as needsProducts/needsSuppliers above.
  const needsDeliveries = (definition.fields ?? []).some((f) => f.type === "delivery");
  const needsCustomers = (definition.fields ?? []).some((f) => f.type === "customer");

  const [role, allDefinitions, people, products, suppliers, deliveries, customers] = await Promise.all([
    fetchRoleInFirm(sessionToken, firmId),
    fetchDefinitions(sessionToken, firmId),
    needsPeople ? fetchPeople(sessionToken, firmId) : Promise.resolve(null),
    needsProducts ? fetchProducts(sessionToken, firmId) : Promise.resolve(null),
    needsSuppliers ? fetchSuppliers(sessionToken, firmId) : Promise.resolve(null),
    needsDeliveries ? fetchDeliveries(sessionToken, firmId) : Promise.resolve(null),
    needsCustomers ? fetchCustomers(sessionToken, firmId) : Promise.resolve(null),
  ]);
  const isOwner = role?.isOwner ?? false;
  const rules = isOwner || role ? await fetchRules(sessionToken, firmId, workflowKey) : null;
  const availableActions: AvailableAction[] = Array.from(
    new Map(
      [...(instancesPage?.instances ?? []), ...allInstances].flatMap((inst) =>
        inst.availableActions.map((a) => [a.actionKey, a] as const),
      ),
    ).values(),
  );

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="flex w-full max-w-2xl flex-col items-center gap-6">
        {/* definition.name is workflow_definitions.name from the
            backend - data, not UI copy, same convention as workflow
            state/action names elsewhere in this component tree. */}
        <div className="w-full">
          <ListPageHeader
            title={definition.name}
            breadcrumb={[
              { label: tNav("brand"), href: "/" },
              { label: tNav("workflows"), href: "/workflows" },
              { label: definition.name },
            ]}
          />
        </div>

        <CreateInstanceForm
          firmId={firmId}
          definitionId={definition.definitionId}
          createPermissionKey={definition.createPermissionKey}
          fields={definition.fields}
          people={people ?? undefined}
          products={products ?? undefined}
          suppliers={suppliers ?? undefined}
          deliveries={deliveries ?? undefined}
          customers={customers ?? undefined}
        />

        <WorkflowInstanceList
          firmId={firmId}
          instances={instancesPage?.instances ?? []}
          total={instancesPage?.total ?? 0}
          page={safePage}
          pageSize={INSTANCES_PAGE_SIZE}
          q={q}
        />
      </div>

      {definition.transitions?.some((tr) => tr.journal) && (
        <div className="flex w-full max-w-2xl flex-col gap-3">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("journalTemplatesTitle")}
          </h2>
          <ul className="flex flex-col gap-3">
            {definition.transitions
              .filter((tr) => tr.journal)
              .map((tr) => (
                <li
                  key={tr.actionKey}
                  className="rounded-md border border-zinc-300 p-3 text-sm dark:border-zinc-700"
                >
                  <div className="flex flex-wrap items-baseline justify-between gap-2">
                    <span className="font-medium text-black dark:text-zinc-50">{tr.name}</span>
                    <span className="text-xs text-zinc-500 dark:text-zinc-400">
                      {tr.fromState.name} → {tr.toState.name}
                    </span>
                  </div>
                  <p className="mt-1 text-xs text-zinc-600 dark:text-zinc-400">
                    {tr.journal!.description}
                  </p>
                  <ul className="mt-2 flex flex-col gap-1">
                    {tr.journal!.lines.map((line, idx) => (
                      <li
                        key={idx}
                        className="flex items-center justify-between font-mono text-xs text-zinc-700 dark:text-zinc-300"
                      >
                        <span>
                          {line.side === "debit" ? t("debitLabel") : t("creditLabel")}{" "}
                          {line.accountCode}
                        </span>
                        <span>{line.amountField}</span>
                      </li>
                    ))}
                  </ul>
                </li>
              ))}
          </ul>
        </div>
      )}

      {rules !== null && (
        <div className="flex w-full max-w-2xl flex-col items-center gap-3">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("ruleListTitle")}
          </h2>
          {isOwner && (
            <RuleBuilder
              firmId={firmId}
              definitionKey={workflowKey}
              fields={definition.fields}
              availableActions={availableActions}
              existingDefinitionKeys={(allDefinitions ?? [])
                .map((d) => d.key)
                .filter((key) => key !== workflowKey)}
            />
          )}
          <RuleList firmId={firmId} definitionKey={workflowKey} rules={rules} isOwner={isOwner} />
        </div>
      )}

      {auditEntries && (
        <div className="w-full max-w-2xl">
          <WorkflowHistory instances={allInstances} entries={auditEntries} />
        </div>
      )}
    </main>
  );
}
