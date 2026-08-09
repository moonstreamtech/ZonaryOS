// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Edge Agent protocol foundation, frontend side (internal/edgeagent) -
// mirrors lib/logistics.ts's own "types + fetch helpers, one file per
// backend package" convention. Only the Keycloak-authenticated
// browser-facing surface lives here: registering agents, issuing/listing
// tokens, listing events. The token-authenticated device path
// (/api/edge/heartbeat, /api/edge/events) has no frontend caller.

export type AgentStatus = "offline" | "online" | "error";

export type EdgeAgent = {
  id: string;
  name: string;
  description?: string;
  status: AgentStatus;
  lastSeenAt?: string;
  version?: string;
  capabilities: string[];
  config: Record<string, unknown>;
  createdAt: string;
};

export type EdgeAgentToken = {
  id: string;
  agentId: string;
  name: string;
  lastUsedAt?: string;
  expiresAt?: string;
  createdAt: string;
};

export type EdgeEvent = {
  id: string;
  agentId: string;
  eventType: string;
  payload: Record<string, unknown>;
  receivedAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/edge-agents`. */
export async function fetchEdgeAgents(token: string, firmId: string): Promise<EdgeAgent[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/edge-agents`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as EdgeAgent[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/edge-agents/{agentId}`. */
export async function fetchEdgeAgent(token: string, firmId: string, agentId: string): Promise<EdgeAgent | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/edge-agents/${encodeURIComponent(agentId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as EdgeAgent;
  } catch {
    return null;
  }
}

export type CreateEdgeAgentInput = {
  name: string;
  description?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/edge-agents` (owner-only). */
export async function createEdgeAgent(
  token: string,
  firmId: string,
  input: CreateEdgeAgentInput,
): Promise<{ ok: true; agent: EdgeAgent } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/edge-agents`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, agent: (await res.json()) as EdgeAgent };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/edge-agents/{agentId}/tokens` (owner-only). */
export async function fetchEdgeAgentTokens(
  token: string,
  firmId: string,
  agentId: string,
): Promise<EdgeAgentToken[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/edge-agents/${encodeURIComponent(agentId)}/tokens`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as EdgeAgentToken[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/edge-agents/{agentId}/tokens`
 * (owner-only). The plaintext token in the response is shown to the caller
 * exactly once - ZonaryOS itself never has it again after this call.
 */
export async function issueEdgeAgentToken(
  token: string,
  firmId: string,
  agentId: string,
  name: string,
): Promise<{ ok: true; plaintext: string; summary: EdgeAgentToken } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/edge-agents/${encodeURIComponent(agentId)}/tokens`,
      {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    const data = (await res.json()) as { token: string; summary: EdgeAgentToken };
    return { ok: true, plaintext: data.token, summary: data.summary };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/edge-agents/{agentId}/events`. */
export async function fetchEdgeAgentEvents(
  token: string,
  firmId: string,
  agentId: string,
): Promise<EdgeEvent[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/edge-agents/${encodeURIComponent(agentId)}/events`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as EdgeEvent[];
  } catch {
    return null;
  }
}
