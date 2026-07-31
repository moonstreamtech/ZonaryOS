"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter, usePathname, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import type { AuditLogEntry } from "@/lib/auditlog";
import type { WorkflowDefinition } from "@/lib/workflow";

type Props = {
  entries: AuditLogEntry[];
  total: number;
  page: number;
  pageSize: number;
  from: string;
  to: string;
  definitionId: string;
  definitions: WorkflowDefinition[];
};

// Pagination + date-range/definition filter controls for the audit log,
// same URL-driven shape as
// components/Workflow/WorkflowInstanceList.tsx's pagination/search (the
// stock-list pagination this batch was told to reuse rather than
// reinvent): page/from/to/definitionId all live in the URL's search
// params, not client state, so the page stays shareable/bookmarkable and
// survives a refresh.
export default function AuditLogTable({
  entries,
  total,
  page,
  pageSize,
  from,
  to,
  definitionId,
  definitions,
}: Props) {
  const t = useTranslations("AuditLog");
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const [fromInput, setFromInput] = useState(from);
  const [toInput, setToInput] = useState(to);
  const [definitionInput, setDefinitionInput] = useState(definitionId);

  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const filtersActive = Boolean(from || to || definitionId);

  // Builds a URL for pathname carrying whatever page/from/to/definitionId
  // this navigation wants, dropping any that are empty - same "reset page
  // back to 1 whenever the filters themselves change" convention
  // WorkflowInstanceList's own navigate() uses for search.
  function navigate(next: { page?: number; from?: string; to?: string; definitionId?: string }) {
    const params = new URLSearchParams(searchParams.toString());
    const nextPage = next.page ?? 1;
    if (nextPage > 1) params.set("page", String(nextPage));
    else params.delete("page");

    const nextFrom = next.from ?? "";
    if (nextFrom) params.set("from", nextFrom);
    else params.delete("from");

    const nextTo = next.to ?? "";
    if (nextTo) params.set("to", nextTo);
    else params.delete("to");

    const nextDefinitionId = next.definitionId ?? "";
    if (nextDefinitionId) params.set("definitionId", nextDefinitionId);
    else params.delete("definitionId");

    const qs = params.toString();
    router.push(qs ? `${pathname}?${qs}` : pathname);
  }

  function submitFilters(e: FormEvent) {
    e.preventDefault();
    // datetime-local inputs give "YYYY-MM-DDTHH:mm" with no timezone -
    // append seconds/UTC offset so the backend's RFC3339 parser
    // (internal/auditlog.parseListOptions) accepts it as-is.
    const toRFC3339 = (v: string) => (v ? new Date(v).toISOString() : "");
    navigate({
      page: 1,
      from: toRFC3339(fromInput),
      to: toRFC3339(toInput),
      definitionId: definitionInput,
    });
  }

  function clearFilters() {
    setFromInput("");
    setToInput("");
    setDefinitionInput("");
    navigate({ page: 1, from: "", to: "", definitionId: "" });
  }

  return (
    <div className="flex w-full max-w-4xl flex-col items-center gap-4">
      <form
        onSubmit={submitFilters}
        className="flex w-full flex-wrap items-end gap-3"
      >
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("filtersFromLabel")}
          <input
            type="datetime-local"
            value={fromInput}
            onChange={(e) => setFromInput(e.target.value)}
            className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("filtersToLabel")}
          <input
            type="datetime-local"
            value={toInput}
            onChange={(e) => setToInput(e.target.value)}
            className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("filtersDefinitionLabel")}
          <select
            value={definitionInput}
            onChange={(e) => setDefinitionInput(e.target.value)}
            className="rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
          >
            <option value="">{t("filtersDefinitionAll")}</option>
            {definitions.map((definition) => (
              // definition.name is workflow_definitions.name from the
              // backend - data, not UI copy, same convention as
              // WorkflowInstanceList's own state/action names.
              <option key={definition.definitionId} value={definition.definitionId}>
                {definition.name}
              </option>
            ))}
          </select>
        </label>
        <button
          type="submit"
          data-permission-public="true"
          className="rounded-md bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
        >
          {t("filtersApplyButton")}
        </button>
        {filtersActive && (
          <button
            type="button"
            onClick={clearFilters}
            data-permission-public="true"
            className="rounded-md border border-zinc-300 px-4 py-1.5 text-xs font-medium text-zinc-700 dark:border-zinc-700 dark:text-zinc-300"
          >
            {t("filtersClearButton")}
          </button>
        )}
      </form>

      {entries.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">
          {filtersActive ? t("filteredEmpty") : t("empty")}
        </p>
      ) : (
        <div className="w-full overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("columnWhen")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnWho")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnEntityType")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnEntityId")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnAction")}</th>
                <th className="py-2 font-medium">{t("columnChanges")}</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => (
                <tr
                  key={entry.id}
                  className="border-b border-zinc-200 align-top text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4 whitespace-nowrap">
                    {new Date(entry.occurredAt).toLocaleString()}
                  </td>
                  <td className="py-2 pr-4">
                    {entry.userDisplayName || entry.userEmail}
                  </td>
                  {/* entityType/action are audit_log.entity_type/action
                      values from the backend - data, not UI copy, same
                      convention StockList.tsx documents for workflow
                      state/action names. */}
                  <td className="py-2 pr-4">{entry.entityType}</td>
                  <td className="py-2 pr-4 font-mono text-xs">
                    {entry.entityId}
                  </td>
                  <td className="py-2 pr-4">{entry.action}</td>
                  <td className="py-2 font-mono text-xs whitespace-pre-wrap">
                    {JSON.stringify(entry.changes)}
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
            disabled={page <= 1}
            onClick={() => navigate({ page: page - 1, from, to, definitionId })}
            data-permission-public="true"
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-xs font-medium disabled:opacity-50 dark:border-zinc-700"
          >
            {t("paginationPrevious")}
          </button>
          <span>{t("paginationSummary", { page, totalPages, total })}</span>
          <button
            type="button"
            disabled={page >= totalPages}
            onClick={() => navigate({ page: page + 1, from, to, definitionId })}
            data-permission-public="true"
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-xs font-medium disabled:opacity-50 dark:border-zinc-700"
          >
            {t("paginationNext")}
          </button>
        </div>
      )}
    </div>
  );
}
