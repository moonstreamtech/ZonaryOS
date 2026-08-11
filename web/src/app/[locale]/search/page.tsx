// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations, setRequestLocale } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchDefinitions, searchAcrossDefinitions } from "@/lib/workflow";
import { searchEntities, type SearchHit } from "@/lib/globalSearch";
import { Link } from "@/i18n/navigation";
import { formatPayload } from "@/components/Workflow/format";
import GlobalSearchBox from "@/components/Nav/GlobalSearchBox";

type PageProps = {
  params: Promise<{ locale: string }>;
  searchParams: Promise<{ q?: string }>;
};

const ENTITY_TYPE_ROUTE: Record<SearchHit["type"], string> = {
  products: "/inventory",
  customers: "/customers",
  people: "/hr",
  suppliers: "/suppliers",
};

const ENTITY_TYPE_LABEL_KEY: Record<SearchHit["type"], "typeProducts" | "typeCustomers" | "typePeople" | "typeSuppliers"> = {
  products: "typeProducts",
  customers: "typeCustomers",
  people: "typePeople",
  suppliers: "typeSuppliers",
};

// Item 3, firm-wide search entry point: not scoped to one workflow
// definition (see /workflows/[key]'s own ?q= search) or to workflows at
// all - this batch (internal/search) extends it to also search
// products/customers/people/suppliers via one cross-entity backend call,
// rendered as a second, type-grouped section alongside the pre-existing
// per-workflow-definition groups (lib/workflow.ts's
// searchAcrossDefinitions, unchanged). Each entity hit links to its own
// list page (customers alone has a real per-record detail route,
// /customers/[customerId] - inventory/hr/suppliers are flat list+edit
// pages with no deep-linkable detail route yet, so those hits land on
// the list page itself rather than inventing one).
//
// Server-rendered like every other firm-scoped page in this app (no
// client-side fetch/spinner - see requireFirmContext's sibling pages):
// the "loading" state is simply the page not having rendered yet,
// consistent with WorkflowDefinitionView's own convention.
export default async function SearchPage({ params, searchParams }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Search");
  const { q: rawQ } = await searchParams;
  const q = (rawQ ?? "").trim();

  const { sessionToken, firm } = await requireFirmContext(locale);
  const definitions = await fetchDefinitions(sessionToken, firm.firmId);

  const [groups, entityHits] = await Promise.all([
    definitions && q ? searchAcrossDefinitions(sessionToken, firm.firmId, definitions, q) : Promise.resolve([]),
    q ? searchEntities(sessionToken, firm.firmId, q) : Promise.resolve([]),
  ]);
  const hitsByType = new Map<SearchHit["type"], SearchHit[]>();
  for (const hit of entityHits ?? []) {
    const list = hitsByType.get(hit.type) ?? [];
    list.push(hit);
    hitsByType.set(hit.type, list);
  }
  const hasAnyResults = groups.length > 0 || hitsByType.size > 0;

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-2xl flex-col items-center gap-4">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
        <GlobalSearchBox initialQuery={q} />
      </div>

      <div className="flex w-full max-w-2xl flex-col gap-6">
        {definitions === null ? (
          <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
        ) : !q ? (
          <p className="text-zinc-600 dark:text-zinc-400">{t("prompt")}</p>
        ) : !hasAnyResults ? (
          <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
        ) : (
          <>
            {groups.length > 0 && (
              <div className="flex flex-col gap-4">
                <h2 className="text-xs font-semibold uppercase tracking-wide text-zinc-500 dark:text-zinc-500">
                  {t("workflowResultsTitle")}
                </h2>
                {groups.map((group) => (
                  <section
                    key={group.definition.definitionId}
                    className="flex flex-col gap-2 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
                  >
                    {/* group.definition.name is workflow_definitions.name
                        from the backend - data, not UI copy, same
                        convention as elsewhere in this component tree. */}
                    <h3 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
                      {group.definition.name}
                    </h3>
                    <ul className="flex flex-col gap-2">
                      {group.instances.map((instance) => (
                        <li key={instance.instanceId}>
                          <Link
                            href={`/workflows/${group.definition.key}?q=${encodeURIComponent(q)}`}
                            data-permission-public="true"
                            className="block rounded-md border border-zinc-200 px-3 py-2 text-sm text-black hover:border-zinc-400 dark:border-zinc-800 dark:text-zinc-50 dark:hover:border-zinc-600"
                          >
                            <span>
                              {formatPayload(instance.payload) || (
                                <span className="text-zinc-400 dark:text-zinc-600">
                                  —
                                </span>
                              )}
                            </span>
                            {/* instance.state.name is workflow_states.name
                                from the backend - data, not UI copy. */}
                            <span className="ml-2 text-zinc-500 dark:text-zinc-400">
                              ({instance.state.name})
                            </span>
                          </Link>
                        </li>
                      ))}
                    </ul>
                    {group.total > group.instances.length && (
                      <Link
                        href={`/workflows/${group.definition.key}?q=${encodeURIComponent(q)}`}
                        data-permission-public="true"
                        className="text-xs font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
                      >
                        {t("seeAllInWorkflow", {
                          count: group.total,
                          name: group.definition.name,
                        })}
                      </Link>
                    )}
                  </section>
                ))}
              </div>
            )}

            {hitsByType.size > 0 && (
              <div className="flex flex-col gap-4">
                <h2 className="text-xs font-semibold uppercase tracking-wide text-zinc-500 dark:text-zinc-500">
                  {t("entityResultsTitle")}
                </h2>
                {Array.from(hitsByType.entries()).map(([type, hits]) => (
                  <section
                    key={type}
                    className="flex flex-col gap-2 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
                  >
                    <h3 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
                      {t(ENTITY_TYPE_LABEL_KEY[type])}
                    </h3>
                    <ul className="flex flex-col gap-2">
                      {hits.map((hit) => (
                        <li key={hit.id}>
                          <Link
                            href={type === "customers" ? `/customers/${hit.id}` : ENTITY_TYPE_ROUTE[type]}
                            data-permission-public="true"
                            className="block rounded-md border border-zinc-200 px-3 py-2 text-sm text-black hover:border-zinc-400 dark:border-zinc-800 dark:text-zinc-50 dark:hover:border-zinc-600"
                          >
                            <span>{hit.title}</span>
                            {hit.subtitle && (
                              <span className="ml-2 text-zinc-500 dark:text-zinc-400">({hit.subtitle})</span>
                            )}
                          </Link>
                        </li>
                      ))}
                    </ul>
                  </section>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </main>
  );
}
