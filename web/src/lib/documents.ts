// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Document templates foundation, frontend side (internal/documents) -
// mirrors lib/localization.ts's own "types + fetch helpers" convention.

export type DocumentTemplateType = "invoice" | "delivery_note" | "report";

export type DocumentTemplate = {
  id: string;
  name: string;
  type: DocumentTemplateType;
  template: string;
  isDefault: boolean;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

export async function fetchDocumentTemplates(token: string, firmId: string): Promise<DocumentTemplate[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/document-templates`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as DocumentTemplate[];
  } catch {
    return null;
  }
}

export type DocumentTemplateInput = Omit<DocumentTemplate, "id" | "createdAt">;

export async function createDocumentTemplate(
  token: string,
  firmId: string,
  input: DocumentTemplateInput,
): Promise<{ ok: true; template: DocumentTemplate } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/document-templates`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) return { ok: false, status: res.status };
    return { ok: true, template: (await res.json()) as DocumentTemplate };
  } catch {
    return { ok: false, status: 0 };
  }
}

export async function updateDocumentTemplate(
  token: string,
  firmId: string,
  templateId: string,
  input: DocumentTemplateInput,
): Promise<{ ok: true; template: DocumentTemplate } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/document-templates/${encodeURIComponent(templateId)}`, {
      method: "PUT",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) return { ok: false, status: res.status };
    return { ok: true, template: (await res.json()) as DocumentTemplate };
  } catch {
    return { ok: false, status: 0 };
  }
}
