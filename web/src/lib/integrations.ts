// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Data pipeline / ETL / system integrations, frontend side
// (internal/integration) - mirrors lib/webhooks.ts's and lib/aiConfig.ts's
// own "types + fetch helpers, one file per backend package" convention.

export const CONNECTOR_TYPES = ["http_webhook", "ftp", "sftp", "database", "email_imap", "custom"] as const;
export type ConnectorType = (typeof CONNECTOR_TYPES)[number];

export const SYNC_DIRECTIONS = ["inbound", "outbound", "bidirectional"] as const;
export type SyncDirection = (typeof SYNC_DIRECTIONS)[number];

export const SYNC_STATUSES = ["running", "success", "partial", "failed"] as const;
export type SyncStatus = (typeof SYNC_STATUSES)[number];

// The fixed set of connector config keys internal/integration/crypto.go's
// own sensitiveConfigKeys encrypts at rest and masks on read - any other
// config key (url, username, pollIntervalMinutes, ...) round-trips as
// plain text. Kept in sync with that map by hand (it's a closed, rarely-
// changed vocabulary on the Go side too).
export const SENSITIVE_CONFIG_KEYS = ["apiKey", "password", "secret", "token"] as const;
export type SensitiveConfigKey = (typeof SENSITIVE_CONFIG_KEYS)[number];

export function isSensitiveConfigKey(key: string): key is SensitiveConfigKey {
  return (SENSITIVE_CONFIG_KEYS as readonly string[]).includes(key);
}

export type Connector = {
  id: string;
  name: string;
  type: ConnectorType;
  // Every sensitive key (see SENSITIVE_CONFIG_KEYS) is already masked by
  // the server ("...WXYZ", last 4 characters only, or "" if never set) -
  // this Config never carries a real secret, see crypto.go's own
  // maskSensitiveConfigFields doc comment.
  config: Record<string, unknown>;
  isActive: boolean;
  lastSyncAt?: string;
  lastSyncStatus?: string;
  createdAt: string;
};

export type ConnectorInput = {
  name: string;
  type: ConnectorType;
  config: Record<string, unknown>;
  isActive: boolean;
};

// FieldMapping's wire shape is snake_case (source_field/target_field/
// transform) - internal/integration/transform.go's own FieldMapping
// struct tags, unlike every other field on this page's connector/mapping
// responses (which are camelCase) - it's stored and round-tripped
// verbatim as integration_mappings.field_mappings jsonb, not re-encoded
// through a Go struct with json:"...,camelCase" tags at the handler
// layer the way the rest of connectorResponse/mappingResponse are.
export type FieldMapping = {
  source_field: string;
  target_field: string;
  // Either a single transform name ("uppercase"), or an ordered list
  // ("trim","uppercase") - see transform.go's own applyOne doc comment
  // for the full supported list. Omitted/empty means no transform.
  transform?: string | string[];
};

export type Mapping = {
  id: string;
  connectorId: string;
  sourceEntity: string;
  targetEntity: string;
  fieldMappings: FieldMapping[];
  syncDirection: SyncDirection;
  isActive: boolean;
  createdAt: string;
};

export type MappingInput = {
  connectorId: string;
  sourceEntity: string;
  targetEntity: string;
  fieldMappings: FieldMapping[];
  syncDirection: SyncDirection;
  isActive: boolean;
};

export type SyncLog = {
  id: string;
  connectorId: string;
  mappingId?: string;
  startedAt: string;
  finishedAt?: string;
  recordsProcessed: number;
  recordsFailed: number;
  status: SyncStatus;
  errorLog?: string[];
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/integration-connectors` (member-only). */
export async function fetchConnectors(token: string, firmId: string): Promise<Connector[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-connectors`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Connector[];
  } catch {
    return null;
  }
}

type ActionResult<T> = { ok: true; value: T } | { ok: false; error: string; status: number };

async function post<T>(token: string, url: string, body: unknown): Promise<ActionResult<T>> {
  try {
    const res = await fetch(url, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    if (res.status === 204) return { ok: true, value: undefined as T };
    return { ok: true, value: (await res.json()) as T };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

async function patch<T>(token: string, url: string, body: unknown): Promise<ActionResult<T>> {
  try {
    const res = await fetch(url, {
      method: "PATCH",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, value: (await res.json()) as T };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

async function del(token: string, url: string): Promise<boolean> {
  try {
    const res = await fetch(url, { method: "DELETE", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" });
    return res.ok;
  } catch {
    return false;
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/integration-connectors` (owner-only). */
export async function createConnector(token: string, firmId: string, input: ConnectorInput) {
  return post<Connector>(token, `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-connectors`, input);
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/integration-connectors/{connectorId}` (owner-only). */
export async function updateConnector(token: string, firmId: string, connectorId: string, input: ConnectorInput) {
  return patch<Connector>(
    token,
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-connectors/${encodeURIComponent(connectorId)}`,
    input,
  );
}

/** Calls the Go backend's `DELETE /api/firms/{firmId}/integration-connectors/{connectorId}` (owner-only). */
export async function deleteConnector(token: string, firmId: string, connectorId: string) {
  return del(
    token,
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-connectors/${encodeURIComponent(connectorId)}`,
  );
}

/** Calls the Go backend's `POST /api/firms/{firmId}/integration-connectors/{connectorId}/test` (owner-only). */
export async function testConnector(token: string, firmId: string, connectorId: string) {
  return post<undefined>(
    token,
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-connectors/${encodeURIComponent(connectorId)}/test`,
    {},
  );
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/integration-mappings`
 * (member-only), optionally filtered to one connector via the
 * `connectorId` query param (see internal/integration/handlers.go's
 * handleListMappings).
 */
export async function fetchMappings(token: string, firmId: string, connectorId?: string): Promise<Mapping[] | null> {
  try {
    const qs = connectorId ? `?connectorId=${encodeURIComponent(connectorId)}` : "";
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-mappings${qs}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Mapping[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/integration-mappings` (owner-only). */
export async function createMapping(token: string, firmId: string, input: MappingInput) {
  return post<Mapping>(token, `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-mappings`, input);
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/integration-mappings/{mappingId}` (owner-only). */
export async function updateMapping(token: string, firmId: string, mappingId: string, input: MappingInput) {
  return patch<Mapping>(
    token,
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-mappings/${encodeURIComponent(mappingId)}`,
    input,
  );
}

/** Calls the Go backend's `DELETE /api/firms/{firmId}/integration-mappings/{mappingId}` (owner-only). */
export async function deleteMapping(token: string, firmId: string, mappingId: string) {
  return del(
    token,
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-mappings/${encodeURIComponent(mappingId)}`,
  );
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/integration-sync-logs`
 * (member-only), optionally filtered to one connector via the
 * `connectorId` query param.
 */
export async function fetchSyncLogs(token: string, firmId: string, connectorId?: string): Promise<SyncLog[] | null> {
  try {
    const qs = connectorId ? `?connectorId=${encodeURIComponent(connectorId)}` : "";
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/integration-sync-logs${qs}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as SyncLog[];
  } catch {
    return null;
  }
}
