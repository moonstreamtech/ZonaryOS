"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Contract, ContractStatus, ContractDocument, ContractDocumentType } from "@/lib/contract";

type Props = {
  firmId: string;
  contract: Contract;
  documents: ContractDocument[];
  isOwner: boolean;
};

const DOCUMENT_TYPES: ContractDocumentType[] = ["original", "amendment", "annex", "correspondence"];

function statusBadgeClass(status: ContractStatus): string {
  switch (status) {
    case "active":
      return "bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-400";
    case "review":
      return "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-400";
    case "draft":
      return "bg-zinc-200 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300";
    case "expired":
    case "terminated":
      return "bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-400";
    case "renewed":
      return "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-400";
  }
}

// The /contracts/[contractId] detail page: full contract fields, its
// document list (owner-gated "add document" form), and a "Preview" link
// that opens the contract-render endpoint in a new tab. Mirrors
// AssetDetail.tsx's own layout/action conventions. Date fields display
// the raw "YYYY-MM-DD" string as-is - no UI here sets startDate/endDate,
// only the create form on /contracts does. signedAt is displayed
// read-only (it's a genuine RFC3339 timestamp on the backend, not a
// plain date - see lib/contract.ts's own doc comment); no UI in this
// batch sets it.
export default function ContractDetail({ firmId, contract, documents, isOwner }: Props) {
  const t = useTranslations("Contracts");
  const router = useRouter();

  const [addingDocument, setAddingDocument] = useState(false);
  const [name, setName] = useState("");
  const [documentUrl, setDocumentUrl] = useState("");
  const [type, setType] = useState<ContractDocumentType>("original");
  const [documentError, setDocumentError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitAddDocument(e: FormEvent) {
    e.preventDefault();
    setDocumentError(null);
    if (!name.trim()) {
      setDocumentError(t("documentCreateRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/contracts/${firmId}/${contract.id}/documents`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: name.trim(),
          documentUrl: documentUrl.trim() || undefined,
          type,
        }),
      });
      if (!res.ok) {
        setDocumentError(t("documentCreateError"));
        return;
      }
      setAddingDocument(false);
      setName("");
      setDocumentUrl("");
      setType("original");
      router.refresh();
    } catch {
      setDocumentError(t("documentCreateError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-2xl flex-col gap-6">
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{contract.title}</h1>
        <div className="flex items-center gap-2">
          <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusBadgeClass(contract.status)}`}>
            {t(`statuses.${contract.status}`)}
          </span>
          <a
            href={`/api/contracts/${firmId}/${contract.id}/render`}
            target="_blank"
            rel="noopener noreferrer"
            data-permission-public="true"
            className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {t("preview")}
          </a>
        </div>
      </div>

      <div className="panel flex flex-col gap-2 p-4 text-sm">
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("referenceNumber")}</span>
          <span className="font-mono">{contract.referenceNumber}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("type")}</span>
          <span>{t(`types.${contract.type}`)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("counterpartyName")}</span>
          <span>{contract.counterpartyName}</span>
        </div>
        {contract.counterpartyType && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("counterpartyType")}</span>
            <span>{t(`counterpartyTypes.${contract.counterpartyType}`)}</span>
          </div>
        )}
        {contract.value && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("value")}</span>
            <span className="font-mono">
              {contract.value} {contract.currency}
            </span>
          </div>
        )}
        {contract.startDate && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("startDate")}</span>
            <span>{contract.startDate}</span>
          </div>
        )}
        {contract.endDate && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("endDate")}</span>
            <span>{contract.endDate}</span>
          </div>
        )}
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("autoRenewal")}</span>
          <span>{contract.autoRenewal ? t("yes") : t("no")}</span>
        </div>
        {contract.renewalNoticeDays !== undefined && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("renewalNoticeDays")}</span>
            <span className="font-mono">{contract.renewalNoticeDays}</span>
          </div>
        )}
        {contract.signedAt && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("signedAt")}</span>
            <span>{new Date(contract.signedAt).toLocaleString()}</span>
          </div>
        )}
        {contract.notes && (
          <div className="flex flex-col gap-1 border-t border-zinc-200 pt-2 dark:border-zinc-800">
            <span className="text-zinc-600 dark:text-zinc-400">{t("notes")}</span>
            <span>{contract.notes}</span>
          </div>
        )}
      </div>

      <div className="panel flex flex-col gap-3 p-4">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("documentsTitle")}</h2>
          {isOwner && (
            <button
              type="button"
              data-permission-public="true"
              onClick={() => setAddingDocument((v) => !v)}
              className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            >
              {addingDocument ? t("cancel") : t("addDocument")}
            </button>
          )}
        </div>

        {addingDocument && (
          <form
            onSubmit={submitAddDocument}
            data-permission-public="true"
            className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
          >
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <label className="flex flex-col gap-1 text-sm">
                {t("documentName")}
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("documentType")}
                <select
                  value={type}
                  onChange={(e) => setType(e.target.value as ContractDocumentType)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                >
                  {DOCUMENT_TYPES.map((dt) => (
                    <option key={dt} value={dt}>
                      {t(`documentTypes.${dt}`)}
                    </option>
                  ))}
                </select>
              </label>
              <label className="flex flex-col gap-1 text-sm sm:col-span-2">
                {t("documentUrl")}
                <input
                  value={documentUrl}
                  onChange={(e) => setDocumentUrl(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
            </div>
            {documentError && <p className="text-sm text-red-600 dark:text-red-400">{documentError}</p>}
            <button
              type="submit"
              data-permission-public="true"
              disabled={submitting}
              className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
            >
              {t("create")}
            </button>
          </form>
        )}

        {documents.length === 0 ? (
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("documentsEmpty")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium">{t("documentName")}</th>
                  <th className="py-2 pr-4 font-medium">{t("documentType")}</th>
                  <th className="py-2 pr-4 font-medium">{t("uploadedAt")}</th>
                </tr>
              </thead>
              <tbody>
                {documents.map((d) => (
                  <tr key={d.id} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                    <td className="py-2 pr-4">
                      {d.documentUrl ? (
                        <a
                          href={d.documentUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          data-permission-public="true"
                          className="underline-offset-4 hover:underline"
                        >
                          {d.name}
                        </a>
                      ) : (
                        d.name
                      )}
                    </td>
                    <td className="py-2 pr-4">{t(`documentTypes.${d.type}`)}</td>
                    <td className="py-2 pr-4">{new Date(d.uploadedAt).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
