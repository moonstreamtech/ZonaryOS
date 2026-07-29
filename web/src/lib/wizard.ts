// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

export type WizardAnswer = {
  value: string;
  labelKey: string;
};

export type WizardFirmResult = {
  firmId: string;
  firmName: string;
};

export type WizardNode = {
  key: string;
  kind: "question" | "action" | "placeholder";
  questionKey?: string;
  answers?: WizardAnswer[];
  placeholderKey?: string;
  result?: WizardFirmResult;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/wizard/nodes/{nodeKey}` with token as a
 * Bearer token, mirroring lib/me.ts's fetchMe. Returns null on any
 * failure (missing/expired token, backend unreachable, unknown node)
 * rather than throwing.
 */
export async function fetchWizardNode(
  token: string,
  nodeKey: string,
): Promise<WizardNode | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/wizard/nodes/${encodeURIComponent(nodeKey)}`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as WizardNode;
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `POST /api/wizard/nodes/{nodeKey}/answer`. Unlike
 * fetchWizardNode, failures are surfaced to the caller (e.g. a blank firm
 * name) rather than swallowed, since the wizard UI needs to show the
 * caller what went wrong.
 */
export async function postWizardAnswer(
  token: string,
  nodeKey: string,
  answer: string,
  firmName?: string,
): Promise<{ ok: true; node: WizardNode } | { ok: false; error: string }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/wizard/nodes/${encodeURIComponent(nodeKey)}/answer`,
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ answer, firmName }),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})` };
    }
    return { ok: true, node: (await res.json()) as WizardNode };
  } catch {
    return { ok: false, error: "network error" };
  }
}
