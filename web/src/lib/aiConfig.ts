// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { QuerySpec } from "@/lib/reports";

// AI integration layer, frontend side (internal/ai) - mirrors
// lib/edgeAgents.ts's own "types + fetch helpers, one file per backend
// package" convention. Vision §5: AI is a helper layer that only ever
// suggests - the caller reviews and edits before anything is submitted
// (Never-Violate Rule 9), which is why every suggest-* helper below
// returns the raw suggestion for a form to pre-fill, never something
// that gets submitted on the caller's behalf.

export type AIProvider = "anthropic" | "openai" | "google" | "custom";

export type AIConfiguration = {
  id: string;
  provider: AIProvider;
  model: string;
  apiKeyMasked: string;
  settings: Record<string, unknown>;
  isActive: boolean;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/ai-config` (owner-only). */
export async function fetchAIConfigs(token: string, firmId: string): Promise<AIConfiguration[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/ai-config`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as AIConfiguration[];
  } catch {
    return null;
  }
}

export type CreateAIConfigInput = {
  provider: AIProvider;
  model: string;
  apiKey: string;
};

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/ai-config` (owner-only).
 * The response never carries the plaintext key back, only apiKeyMasked
 * (see internal/ai/handlers.go's configResponse doc comment) - this
 * helper never has the plaintext key past the single call that sends it.
 */
export async function createAIConfig(
  token: string,
  firmId: string,
  input: CreateAIConfigInput,
): Promise<{ ok: true; config: AIConfiguration } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/ai-config`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, config: (await res.json()) as AIConfiguration };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `DELETE /api/firms/{firmId}/ai-config/{configId}` (owner-only). */
export async function deleteAIConfig(token: string, firmId: string, configId: string): Promise<boolean> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/ai-config/${encodeURIComponent(configId)}`,
      { method: "DELETE", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    return res.ok;
  } catch {
    return false;
  }
}

// ---------------------------------------------------------------------
// Suggest-workflow: internal/ai.WorkflowSuggestion's own wire shape - a
// 1:1 subset of DefinitionSpecInput's own fields (lib/workflow.ts), so a
// successful suggestion can be handed straight to DefinitionBuilder's
// own state setters without any reshaping.

export type AISuggestedPermission = {
  key: string;
  description: string;
};

export type AISuggestedState = {
  key: string;
  name: string;
  isInitial: boolean;
  isTerminal: boolean;
};

export type AISuggestedTransition = {
  fromStateKey: string;
  toStateKey: string;
  actionKey: string;
  name: string;
  permission: AISuggestedPermission;
};

export type AISuggestedField = {
  name: string;
  type: string;
  required: boolean;
};

export type AIWorkflowSuggestion = {
  key: string;
  name: string;
  createPermission: AISuggestedPermission;
  states: AISuggestedState[];
  transitions: AISuggestedTransition[];
  fields?: AISuggestedField[];
};

// AISuggestResult is the shared result shape for both suggest-* calls -
// three outcomes a caller has to render distinctly (see
// internal/ai/handlers.go's own doc comment on the 404/422 contract):
//
//   - "ok": a usable suggestion, ready to pre-fill a form.
//   - "notConfigured": the firm has no active AI configuration (404) -
//     the caller should point at the AI config page, not show a generic
//     error.
//   - "invalid": the AI's own response text couldn't be used (422) - raw
//     is exactly what the model said, shown to the human reviewer as-is.
//   - "error": anything else (network failure, unexpected status).
export type AISuggestResult<T> =
  | { kind: "ok"; suggestion: T }
  | { kind: "notConfigured" }
  | { kind: "invalid"; raw: string }
  | { kind: "error"; message: string };

/** Calls the Go backend's `POST /api/firms/{firmId}/ai/suggest-workflow` (owner-only). */
export async function suggestWorkflow(
  token: string,
  firmId: string,
  description: string,
): Promise<AISuggestResult<AIWorkflowSuggestion>> {
  return callSuggest<AIWorkflowSuggestion>(token, firmId, "suggest-workflow", { description });
}

// ---------------------------------------------------------------------
// Suggest-report: internal/ai.SuggestReport returns a reports.QuerySpec
// verbatim - reuse lib/reports.ts's own QuerySpec type rather than a
// second copy of the same shape.

export type AIReportSuggestion = QuerySpec;

/** Calls the Go backend's `POST /api/firms/{firmId}/ai/suggest-report` (owner-only). */
export async function suggestReport(
  token: string,
  firmId: string,
  question: string,
): Promise<AISuggestResult<QuerySpec>> {
  return callSuggest<QuerySpec>(token, firmId, "suggest-report", { question });
}

async function callSuggest<T>(
  token: string,
  firmId: string,
  endpoint: "suggest-workflow" | "suggest-report",
  body: Record<string, string>,
): Promise<AISuggestResult<T>> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/ai/${endpoint}`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
      cache: "no-store",
    });
    if (res.status === 404) {
      return { kind: "notConfigured" };
    }
    if (res.status === 422) {
      const raw = await res.text();
      return { kind: "invalid", raw };
    }
    if (!res.ok) {
      const text = await res.text();
      return { kind: "error", message: text || `Request failed (${res.status})` };
    }
    return { kind: "ok", suggestion: (await res.json()) as T };
  } catch {
    return { kind: "error", message: "network error" };
  }
}
