"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useRef, useState, type ChangeEvent } from "react";
import { useTranslations } from "next-intl";
import type { ImportSummary } from "@/lib/portability";

type Props = {
  firmId: string;
};

// Vision §3's import/export engine, frontend side: an "Export" button
// (downloads the firm's full snapshot via GET /api/portability/export/[firmId])
// and an "Import configuration" file upload (POSTs the uploaded file's
// text unmodified to /api/portability/import/[firmId], then renders the
// returned ImportSummary's imported/skipped counts). Rendered only when
// the caller is an owner (checked by the server component that mounts
// this, settings/page.tsx, mirroring FirmMetadataEditor's own
// convention) - both backend endpoints are owner-gated
// (internal/portability.ExportFirm/ImportConfiguration), so a non-owner
// would only ever see a 403 here anyway. Tagged data-permission-public
// rather than data-permission-key for the same reason
// FirmMetadataEditor's own controls are: gated by the structural is_owner
// check, not a permission-catalog entry.
export default function PortabilityPanel({ firmId }: Props) {
  const t = useTranslations("Settings");
  const fileInputRef = useRef<HTMLInputElement>(null);

  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);

  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const [importSummary, setImportSummary] = useState<ImportSummary | null>(null);

  async function handleExport() {
    setExportError(null);
    setExporting(true);
    try {
      const res = await fetch(`/api/portability/export/${encodeURIComponent(firmId)}`);
      if (!res.ok) {
        setExportError(t("exportError"));
        return;
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "zonaryos-export.json";
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch {
      setExportError(t("exportError"));
    } finally {
      setExporting(false);
    }
  }

  async function handleImportFileChange(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;

    setImportError(null);
    setImportSummary(null);
    setImporting(true);
    try {
      const text = await file.text();
      const res = await fetch(`/api/portability/import/${encodeURIComponent(firmId)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: text,
      });
      if (!res.ok) {
        setImportError(
          res.status === 403
            ? t("importPermissionDenied")
            : res.status === 400
              ? t("importInvalidDocument")
              : t("importError"),
        );
        return;
      }
      setImportSummary((await res.json()) as ImportSummary);
    } catch {
      setImportError(t("importError"));
    } finally {
      setImporting(false);
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="mb-3 text-lg font-semibold text-black dark:text-zinc-50">
          {t("exportTitle")}
        </h2>
        <p className="mb-3 text-sm text-zinc-600 dark:text-zinc-400">{t("exportDescription")}</p>
        {exportError && <p className="mb-2 text-xs text-red-600 dark:text-red-400">{exportError}</p>}
        <button
          type="button"
          data-permission-public="true"
          onClick={handleExport}
          disabled={exporting}
          className="rounded-full bg-foreground px-3 py-1 text-xs font-medium text-background disabled:opacity-50 dark:hover:bg-[#ccc]"
        >
          {exporting ? t("exportInProgress") : t("exportButton")}
        </button>
      </div>

      <div>
        <h2 className="mb-3 text-lg font-semibold text-black dark:text-zinc-50">
          {t("importTitle")}
        </h2>
        <p className="mb-3 text-sm text-zinc-600 dark:text-zinc-400">{t("importDescription")}</p>
        {importError && <p className="mb-2 text-xs text-red-600 dark:text-red-400">{importError}</p>}
        <input
          ref={fileInputRef}
          type="file"
          accept="application/json"
          data-permission-public="true"
          onChange={handleImportFileChange}
          disabled={importing}
          className="text-sm text-zinc-700 dark:text-zinc-300"
        />
        {importSummary && (
          <ul className="mt-3 flex flex-col gap-1 text-sm text-black dark:text-zinc-50">
            <li>
              {t("importSummaryWorkflowDefinitions", {
                imported: importSummary.workflowDefinitions.imported,
                skipped: importSummary.workflowDefinitions.skipped,
              })}
            </li>
            <li>
              {t("importSummaryRules", {
                imported: importSummary.rules.imported,
                skipped: importSummary.rules.skipped,
              })}
            </li>
            <li>
              {t("importSummaryAccounts", {
                imported: importSummary.accounts.imported,
                skipped: importSummary.accounts.skipped,
              })}
            </li>
            <li>
              {t("importSummaryProducts", {
                imported: importSummary.products.imported,
                skipped: importSummary.products.skipped,
              })}
            </li>
          </ul>
        )}
      </div>
    </div>
  );
}
