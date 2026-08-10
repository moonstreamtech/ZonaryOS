"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter, usePathname, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { InstanceState } from "@/lib/workflow";
import { formatPayload } from "./format";

type Props = {
  firmId: string;
  instances: InstanceState[];
  // total/page/pageSize/q drive the pagination controls and search box
  // below - server-computed (see WorkflowDefinitionView), not derived
  // from `instances.length` here, since `instances` is only the current
  // page.
  total: number;
  page: number;
  pageSize: number;
  q: string;
};

// Generic replacement for the old stock-specific StockList.tsx: renders
// any workflow definition's instances, driven entirely by what the
// backend returns for each one (state.name, payload, and each
// AvailableAction's own name/actionKey/permissionKey) rather than
// hardcoding "Sell"/"Add Stock" as literal button labels or "Item"/
// "Quantity" as literal columns. A second, structurally different
// workflow definition renders correctly here with zero changes to this
// file - see internal/workflow/workflow_integration_test.go's
// purchaseOrderSpec and this component's own instanceList.test.ts for
// the proof.
export default function WorkflowInstanceList({
  firmId,
  instances,
  total,
  page,
  pageSize,
  q,
}: Props) {
  const t = useTranslations("Workflow");
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const [pendingKey, setPendingKey] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [searchInput, setSearchInput] = useState(q);

  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  // Builds a URL for pathname carrying every current search param except
  // the ones this navigation is meant to change - so e.g. clicking
  // "Next" while a search is active keeps `q` in the URL, and searching
  // resets `page` back to 1 rather than keeping a stale offset.
  function navigate(next: { page?: number; q?: string }) {
    const params = new URLSearchParams(searchParams.toString());
    const nextPage = next.page ?? 1;
    const nextQ = next.q ?? "";
    if (nextPage > 1) params.set("page", String(nextPage));
    else params.delete("page");
    if (nextQ) params.set("q", nextQ);
    else params.delete("q");
    const qs = params.toString();
    router.push(qs ? `${pathname}?${qs}` : pathname);
  }

  function submitSearch(e: FormEvent) {
    e.preventDefault();
    navigate({ page: 1, q: searchInput });
  }

  async function runAction(instanceId: string, actionKey: string) {
    setError(null);
    const pendingId = `${instanceId}:${actionKey}`;
    setPendingKey(pendingId);
    try {
      const res = await fetch(
        `/api/workflow/instances/${instanceId}/transitions/${actionKey}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ firmId }),
        },
      );
      if (!res.ok) {
        setError(
          res.status === 403 ? t("actionPermissionDenied") : t("actionError"),
        );
        return;
      }
      router.refresh();
    } catch {
      setError(t("actionError"));
    } finally {
      setPendingKey(null);
    }
  }

  return (
    <div className="flex w-full flex-col items-center gap-4">
      {error && <p className="text-red-600 dark:text-red-400">{error}</p>}

      {/* Only shown once there's something to search across - an empty
          firm with zero instances doesn't need a search box yet. */}
      {(total > 0 || q) && (
        <form
          onSubmit={submitSearch}
          data-permission-public="true"
          className="flex w-full gap-2"
          role="search"
        >
          <input
            type="text"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder={t("searchPlaceholder")}
            className="w-full rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
          />
          <button
            type="submit"
            data-permission-public="true"
            className="rounded-md bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
          >
            {t("searchButton")}
          </button>
        </form>
      )}

      {instances.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">
          {q ? t("searchEmpty") : t("empty")}
        </p>
      ) : (
        <div className="w-full overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("columnPayload")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnState")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnActions")}</th>
                <th className="py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {instances.map((instance) => (
                <tr
                  key={instance.instanceId}
                  className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4">
                    {formatPayload(instance.payload) || (
                      <span className="text-zinc-400 dark:text-zinc-600">
                        —
                      </span>
                    )}
                  </td>
                  {/* state.name is workflow_states.name from the backend
                      - data, not UI copy, same convention this codebase
                      has used since the original StockList.tsx for
                      workflow state/action names. */}
                  <td className="py-2 pr-4">{instance.state.name}</td>
                  <td className="py-2">
                    {instance.availableActions.length === 0 ? (
                      <span className="text-zinc-400 dark:text-zinc-600">
                        —
                      </span>
                    ) : (
                      <div className="flex flex-wrap gap-2">
                        {instance.availableActions.map((action) => {
                          const pendingId = `${instance.instanceId}:${action.actionKey}`;
                          return (
                            <button
                              key={action.actionKey}
                              type="button"
                              data-permission-key={action.permissionKey}
                              disabled={pendingKey === pendingId}
                              onClick={() =>
                                runAction(instance.instanceId, action.actionKey)
                              }
                              className="rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] disabled:opacity-50 dark:hover:bg-[#ccc]"
                            >
                              {action.name}
                            </button>
                          );
                        })}
                      </div>
                    )}
                  </td>
                  <td className="py-2 text-right">
                    <Link
                      href={`/workflows/instance/${instance.instanceId}`}
                      data-permission-public="true"
                      className="text-xs text-zinc-600 underline-offset-4 hover:underline dark:text-zinc-400"
                    >
                      {t("viewDetail")}
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {total > 0 && totalPages > 1 && (
        <div className="flex w-full items-center justify-between gap-4 text-sm text-zinc-600 dark:text-zinc-400">
          <button
            type="button"
            data-permission-public="true"
            disabled={page <= 1}
            onClick={() => navigate({ page: page - 1, q })}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-xs font-medium disabled:opacity-50 dark:border-zinc-700"
          >
            {t("paginationPrevious")}
          </button>
          <span>
            {t("paginationSummary", { page, totalPages, total })}
          </span>
          <button
            type="button"
            data-permission-public="true"
            disabled={page >= totalPages}
            onClick={() => navigate({ page: page + 1, q })}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-xs font-medium disabled:opacity-50 dark:border-zinc-700"
          >
            {t("paginationNext")}
          </button>
        </div>
      )}
    </div>
  );
}
