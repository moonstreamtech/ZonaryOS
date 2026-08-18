"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import {
  CONNECTOR_TYPES,
  SYNC_DIRECTIONS,
  isSensitiveConfigKey,
  type Connector,
  type ConnectorInput,
  type ConnectorType,
  type FieldMapping,
  type Mapping,
  type SyncDirection,
  type SyncLog,
} from "@/lib/integrations";

type Props = {
  firmId: string;
  connectors: Connector[];
  isOwner: boolean;
};

type Section = "connectors" | "mappings" | "syncLogs";

// The known integration_connectors.config keys this form always offers a
// row for - the fixed set the backend actually understands for
// http_webhook (see internal/integration/inbound.go/outbound.go and
// crypto.go's own sensitiveConfigKeys), pre-seeded so a firm owner
// doesn't have to remember exact key spelling. A row's key can still be
// edited/added freely (ConfigEditor below), which is how ftp/sftp/
// database/email_imap connectors (registered in the data model but not
// yet executed, see connectors.go's own package doc comment) get their
// own, different config keys entered.
const KNOWN_CONFIG_KEYS = ["url", "apiKey", "password", "secret", "token", "pollIntervalMinutes"] as const;

type ConfigRow = { key: string; value: string };

function emptyConfigRows(): ConfigRow[] {
  return KNOWN_CONFIG_KEYS.map((key) => ({ key, value: "" }));
}

// Builds a create/edit form's starting rows from an already-masked
// Connector.config (see lib/integrations.ts's own Connector doc comment)
// - every sensitive key starts BLANK, never pre-filled with the masked
// "...WXYZ" display form (typing nothing and submitting must mean "leave
// as configured", not "set the secret to the literal text
// \"...WXYZ\"" - see configRowsToPayload below for the other half of
// that contract).
function configToRows(config: Record<string, unknown>): ConfigRow[] {
  const rows: ConfigRow[] = KNOWN_CONFIG_KEYS.map((key) => ({
    key,
    value: isSensitiveConfigKey(key) ? "" : config[key] != null ? String(config[key]) : "",
  }));
  for (const [key, value] of Object.entries(config)) {
    if ((KNOWN_CONFIG_KEYS as readonly string[]).includes(key)) continue;
    rows.push({ key, value: isSensitiveConfigKey(key) ? "" : String(value ?? "") });
  }
  return rows;
}

// Turns a ConfigEditor's rows into the config object POSTed/PATCHed to
// the backend.
//
// Sensitive keys (apiKey/password/secret/token) are OMITTED entirely from
// the payload when left blank - never resubmitted as the masked "...WXYZ"
// display value. A masked value is never re-decryptable client-side, so
// re-encrypting it as if it were the real secret would permanently
// corrupt the stored value the next time this connector is edited without
// retyping the secret. internal/integration.UpdateConnector's own
// preserveUnsetSensitiveFields (crypto.go) is the other half of this
// contract: it keeps a sensitive key's existing encrypted value unchanged
// whenever the submitted config omits or blanks that key, so omitting the
// key here is exactly the "leave it as configured" signal the backend
// expects - not a request to clear it.
function configRowsToPayload(rows: ConfigRow[]): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  for (const { key, value } of rows) {
    const trimmedKey = key.trim();
    if (!trimmedKey) continue;
    const trimmedValue = value.trim();
    if (trimmedKey === "pollIntervalMinutes") {
      if (trimmedValue === "") continue;
      const n = Number(trimmedValue);
      if (!Number.isNaN(n)) payload[trimmedKey] = n;
      continue;
    }
    if (trimmedValue !== "") payload[trimmedKey] = trimmedValue;
  }
  return payload;
}

type FieldMappingRow = { sourceField: string; targetField: string; transform: string };

function emptyFieldMappingRow(): FieldMappingRow {
  return { sourceField: "", targetField: "", transform: "" };
}

function fieldMappingsToRows(fieldMappings: FieldMapping[]): FieldMappingRow[] {
  if (fieldMappings.length === 0) return [emptyFieldMappingRow()];
  return fieldMappings.map((fm) => ({
    sourceField: fm.source_field,
    targetField: fm.target_field,
    transform: Array.isArray(fm.transform) ? fm.transform.join(",") : (fm.transform ?? ""),
  }));
}

// Renders one field mapping row's own "transform" text into the wire
// shape transform.go's own TransformChain accepts - a single name
// ("uppercase"), a comma-separated list ("trim,uppercase") that becomes
// an ordered array, or omitted entirely for no transform.
function parseTransform(text: string): string | string[] | undefined {
  const trimmed = text.trim();
  if (!trimmed) return undefined;
  const parts = trimmed
    .split(",")
    .map((p) => p.trim())
    .filter(Boolean);
  if (parts.length <= 1) return parts[0];
  return parts;
}

function rowsToFieldMappings(rows: FieldMappingRow[]): FieldMapping[] {
  return rows
    .filter((r) => r.sourceField.trim() !== "" && r.targetField.trim() !== "")
    .map((r) => {
      const fm: FieldMapping = { source_field: r.sourceField.trim(), target_field: r.targetField.trim() };
      const transform = parseTransform(r.transform);
      if (transform !== undefined) fm.transform = transform;
      return fm;
    });
}

// The /settings/integrations page's client half: three sections
// (connectors, mappings, sync log history) sharing one connector-scoped
// selection. Unlike WebhooksManager/AiConfigManager (whole page owner-
// gated server-side), ListConnectors/ListMappings/ListSyncLogs are all
// member-gated reads on the Go side (connectors.go's own doc comment) -
// so every read-only element here renders for any firm member, and only
// the create/edit/delete/test controls carry `isOwner &&` guards, tagged
// data-permission-public="true" the same way ApiKeysManager.tsx/
// WebhooksManager.tsx tag every is_owner-gated control (a structural
// flag, not a permission-catalog key - see those files' own doc
// comments).
export default function IntegrationsManager({ firmId, connectors, isOwner }: Props) {
  const t = useTranslations("IntegrationsSettings");
  const router = useRouter();

  const [section, setSection] = useState<Section>("connectors");

  // --- Connectors ---------------------------------------------------
  const [name, setName] = useState("");
  const [type, setType] = useState<ConnectorType>("http_webhook");
  const [configRows, setConfigRows] = useState<ConfigRow[]>(emptyConfigRows());
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [editingConnectorId, setEditingConnectorId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editType, setEditType] = useState<ConnectorType>("http_webhook");
  const [editConfigRows, setEditConfigRows] = useState<ConfigRow[]>([]);
  const [editActive, setEditActive] = useState(true);
  const [editError, setEditError] = useState<string | null>(null);
  const [savingConnectorId, setSavingConnectorId] = useState<string | null>(null);

  const [pendingDeleteConnectorId, setPendingDeleteConnectorId] = useState<string | null>(null);
  const [connectorRowError, setConnectorRowError] = useState<{ connectorId: string; message: string } | null>(null);

  const [testingConnectorId, setTestingConnectorId] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<{ connectorId: string; ok: boolean; message: string } | null>(null);

  function updateRow(rows: ConfigRow[], setRows: (r: ConfigRow[]) => void, index: number, field: "key" | "value", value: string) {
    setRows(rows.map((row, i) => (i === index ? { ...row, [field]: value } : row)));
  }

  async function submitCreateConnector(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim()) {
      setCreateError(t("createNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const input: ConnectorInput = { name: name.trim(), type, config: configRowsToPayload(configRows), isActive: true };
      const res = await fetch(`/api/integration-connectors/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setName("");
      setType("http_webhook");
      setConfigRows(emptyConfigRows());
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  function startEditConnector(connector: Connector) {
    setEditingConnectorId(connector.id);
    setEditName(connector.name);
    setEditType(connector.type);
    setEditConfigRows(configToRows(connector.config));
    setEditActive(connector.isActive);
    setEditError(null);
  }

  function cancelEditConnector() {
    setEditingConnectorId(null);
    setEditError(null);
  }

  async function submitEditConnector(e: FormEvent, connector: Connector) {
    e.preventDefault();
    setEditError(null);
    if (!editName.trim()) {
      setEditError(t("createNameRequired"));
      return;
    }

    setSavingConnectorId(connector.id);
    try {
      const input: ConnectorInput = {
        name: editName.trim(),
        type: editType,
        config: configRowsToPayload(editConfigRows),
        isActive: editActive,
      };
      const res = await fetch(`/api/integration-connectors/${firmId}/${connector.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      });
      if (!res.ok) {
        setEditError(t("saveError"));
        return;
      }
      setEditingConnectorId(null);
      router.refresh();
    } catch {
      setEditError(t("saveError"));
    } finally {
      setSavingConnectorId(null);
    }
  }

  async function handleDeleteConnector(connectorId: string) {
    setConnectorRowError(null);
    setPendingDeleteConnectorId(connectorId);
    try {
      const res = await fetch(`/api/integration-connectors/${firmId}/${connectorId}`, { method: "DELETE" });
      if (!res.ok) {
        setConnectorRowError({ connectorId, message: t("deleteError") });
        return;
      }
      if (selectedConnectorId === connectorId) setSelectedConnectorId("");
      router.refresh();
    } catch {
      setConnectorRowError({ connectorId, message: t("deleteError") });
    } finally {
      setPendingDeleteConnectorId(null);
    }
  }

  async function handleTestConnector(connectorId: string) {
    setTestResult(null);
    setTestingConnectorId(connectorId);
    try {
      const res = await fetch(`/api/integration-connectors/${firmId}/${connectorId}/test`, { method: "POST" });
      if (res.ok) {
        setTestResult({ connectorId, ok: true, message: t("testSuccess") });
      } else if (res.status === 501) {
        setTestResult({ connectorId, ok: false, message: t("testNotImplemented") });
      } else {
        setTestResult({ connectorId, ok: false, message: t("testError") });
      }
    } catch {
      setTestResult({ connectorId, ok: false, message: t("testError") });
    } finally {
      setTestingConnectorId(null);
    }
  }

  // --- Shared connector selection (mappings + sync logs) -------------
  const [selectedConnectorId, setSelectedConnectorId] = useState<string>("");

  // --- Mappings --------------------------------------------------------
  const [mappings, setMappings] = useState<Mapping[] | null>(null);
  const [loadingMappings, setLoadingMappings] = useState(false);
  const [mappingsError, setMappingsError] = useState(false);

  const [sourceEntity, setSourceEntity] = useState("");
  const [targetEntity, setTargetEntity] = useState("");
  const [syncDirection, setSyncDirection] = useState<SyncDirection>("outbound");
  const [fieldMappingRows, setFieldMappingRows] = useState<FieldMappingRow[]>([emptyFieldMappingRow()]);
  const [mappingCreateError, setMappingCreateError] = useState<string | null>(null);
  const [submittingMapping, setSubmittingMapping] = useState(false);

  const [editingMappingId, setEditingMappingId] = useState<string | null>(null);
  const [editSourceEntity, setEditSourceEntity] = useState("");
  const [editTargetEntity, setEditTargetEntity] = useState("");
  const [editSyncDirection, setEditSyncDirection] = useState<SyncDirection>("outbound");
  const [editFieldMappingRows, setEditFieldMappingRows] = useState<FieldMappingRow[]>([emptyFieldMappingRow()]);
  const [editMappingActive, setEditMappingActive] = useState(true);
  const [mappingEditError, setMappingEditError] = useState<string | null>(null);
  const [savingMappingId, setSavingMappingId] = useState<string | null>(null);

  const [pendingDeleteMappingId, setPendingDeleteMappingId] = useState<string | null>(null);
  const [mappingRowError, setMappingRowError] = useState<{ mappingId: string; message: string } | null>(null);

  async function loadMappings(connectorId: string) {
    if (!connectorId) {
      setMappings(null);
      return;
    }
    setLoadingMappings(true);
    setMappingsError(false);
    try {
      const res = await fetch(`/api/integration-mappings/${firmId}?connectorId=${encodeURIComponent(connectorId)}`);
      if (!res.ok) {
        setMappingsError(true);
        setMappings(null);
        return;
      }
      setMappings((await res.json()) as Mapping[]);
    } catch {
      setMappingsError(true);
      setMappings(null);
    } finally {
      setLoadingMappings(false);
    }
  }

  async function selectConnectorForMappings(connectorId: string) {
    setSelectedConnectorId(connectorId);
    setEditingMappingId(null);
    await loadMappings(connectorId);
  }

  async function submitCreateMapping(e: FormEvent) {
    e.preventDefault();
    setMappingCreateError(null);
    if (!selectedConnectorId) {
      setMappingCreateError(t("selectConnectorFirst"));
      return;
    }
    if (!sourceEntity.trim() || !targetEntity.trim()) {
      setMappingCreateError(t("mappingCreateError"));
      return;
    }
    const fieldMappings = rowsToFieldMappings(fieldMappingRows);
    if (fieldMappings.length === 0) {
      setMappingCreateError(t("mappingFieldMappingsRequired"));
      return;
    }

    setSubmittingMapping(true);
    try {
      const res = await fetch(`/api/integration-mappings/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          connectorId: selectedConnectorId,
          sourceEntity: sourceEntity.trim(),
          targetEntity: targetEntity.trim(),
          fieldMappings,
          syncDirection,
          isActive: true,
        }),
      });
      if (!res.ok) {
        setMappingCreateError(t("mappingCreateError"));
        return;
      }
      setSourceEntity("");
      setTargetEntity("");
      setSyncDirection("outbound");
      setFieldMappingRows([emptyFieldMappingRow()]);
      await loadMappings(selectedConnectorId);
    } catch {
      setMappingCreateError(t("mappingCreateError"));
    } finally {
      setSubmittingMapping(false);
    }
  }

  function startEditMapping(mapping: Mapping) {
    setEditingMappingId(mapping.id);
    setEditSourceEntity(mapping.sourceEntity);
    setEditTargetEntity(mapping.targetEntity);
    setEditSyncDirection(mapping.syncDirection);
    setEditFieldMappingRows(fieldMappingsToRows(mapping.fieldMappings));
    setEditMappingActive(mapping.isActive);
    setMappingEditError(null);
  }

  function cancelEditMapping() {
    setEditingMappingId(null);
    setMappingEditError(null);
  }

  async function submitEditMapping(e: FormEvent, mapping: Mapping) {
    e.preventDefault();
    setMappingEditError(null);
    if (!editSourceEntity.trim() || !editTargetEntity.trim()) {
      setMappingEditError(t("mappingCreateError"));
      return;
    }
    const fieldMappings = rowsToFieldMappings(editFieldMappingRows);
    if (fieldMappings.length === 0) {
      setMappingEditError(t("mappingFieldMappingsRequired"));
      return;
    }

    setSavingMappingId(mapping.id);
    try {
      const res = await fetch(`/api/integration-mappings/${firmId}/${mapping.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          connectorId: mapping.connectorId,
          sourceEntity: editSourceEntity.trim(),
          targetEntity: editTargetEntity.trim(),
          fieldMappings,
          syncDirection: editSyncDirection,
          isActive: editMappingActive,
        }),
      });
      if (!res.ok) {
        setMappingEditError(t("mappingSaveError"));
        return;
      }
      setEditingMappingId(null);
      await loadMappings(selectedConnectorId);
    } catch {
      setMappingEditError(t("mappingSaveError"));
    } finally {
      setSavingMappingId(null);
    }
  }

  async function handleDeleteMapping(mappingId: string) {
    setMappingRowError(null);
    setPendingDeleteMappingId(mappingId);
    try {
      const res = await fetch(`/api/integration-mappings/${firmId}/${mappingId}`, { method: "DELETE" });
      if (!res.ok) {
        setMappingRowError({ mappingId, message: t("mappingDeleteError") });
        return;
      }
      await loadMappings(selectedConnectorId);
    } catch {
      setMappingRowError({ mappingId, message: t("mappingDeleteError") });
    } finally {
      setPendingDeleteMappingId(null);
    }
  }

  // --- Sync logs -------------------------------------------------------
  const [syncLogConnectorId, setSyncLogConnectorId] = useState<string>("");
  const [syncLogs, setSyncLogs] = useState<SyncLog[] | null>(null);
  const [loadingSyncLogs, setLoadingSyncLogs] = useState(false);
  const [syncLogsError, setSyncLogsError] = useState(false);
  const [syncLogsLoaded, setSyncLogsLoaded] = useState(false);

  async function loadSyncLogs(connectorId: string) {
    setLoadingSyncLogs(true);
    setSyncLogsError(false);
    try {
      const qs = connectorId ? `?connectorId=${encodeURIComponent(connectorId)}` : "";
      const res = await fetch(`/api/integration-sync-logs/${firmId}${qs}`);
      if (!res.ok) {
        setSyncLogsError(true);
        setSyncLogs(null);
        return;
      }
      setSyncLogs((await res.json()) as SyncLog[]);
      setSyncLogsLoaded(true);
    } catch {
      setSyncLogsError(true);
      setSyncLogs(null);
    } finally {
      setLoadingSyncLogs(false);
    }
  }

  function switchSection(next: Section) {
    setSection(next);
    if (next === "syncLogs" && !syncLogsLoaded) {
      void loadSyncLogs(syncLogConnectorId);
    }
  }

  function configEditor(
    rows: ConfigRow[],
    setRows: (r: ConfigRow[]) => void,
    hint: string,
  ) {
    return (
      <div className="flex flex-col gap-2">
        <span className="text-sm">{t("config")}</span>
        <p className="text-xs text-zinc-500 dark:text-zinc-400">{hint}</p>
        {rows.map((row, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <input
              value={row.key}
              onChange={(e) => updateRow(rows, setRows, i, "key", e.target.value)}
              placeholder={t("configKeyPlaceholder")}
              className="w-40 rounded-md border border-zinc-300 px-2 py-1 text-xs dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
            <input
              value={row.value}
              onChange={(e) => updateRow(rows, setRows, i, "value", e.target.value)}
              type={isSensitiveConfigKey(row.key) ? "password" : "text"}
              autoComplete="off"
              placeholder={isSensitiveConfigKey(row.key) ? t("sensitiveFieldPlaceholder") : t("configValuePlaceholder")}
              className="min-w-0 flex-1 rounded-md border border-zinc-300 px-2 py-1 text-xs dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
            <button
              type="button"
              data-permission-public="true"
              onClick={() => setRows(rows.filter((_, idx) => idx !== i))}
              className="rounded-md border border-zinc-300 px-2 py-1 text-xs text-black dark:border-zinc-700 dark:text-zinc-50"
            >
              {t("removeConfigField")}
            </button>
          </div>
        ))}
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setRows([...rows, { key: "", value: "" }])}
          className="self-start rounded-md border border-zinc-300 px-2 py-1 text-xs text-black dark:border-zinc-700 dark:text-zinc-50"
        >
          {t("addConfigField")}
        </button>
      </div>
    );
  }

  function fieldMappingEditor(rows: FieldMappingRow[], setRows: (r: FieldMappingRow[]) => void) {
    return (
      <div className="flex flex-col gap-2">
        <span className="text-sm">{t("fieldMappings")}</span>
        <p className="text-xs text-zinc-500 dark:text-zinc-400">{t("transformHelp")}</p>
        {rows.map((row, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <input
              value={row.sourceField}
              onChange={(e) => setRows(rows.map((r, idx) => (idx === i ? { ...r, sourceField: e.target.value } : r)))}
              placeholder={t("sourceField")}
              className="w-32 rounded-md border border-zinc-300 px-2 py-1 text-xs dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
            <input
              value={row.targetField}
              onChange={(e) => setRows(rows.map((r, idx) => (idx === i ? { ...r, targetField: e.target.value } : r)))}
              placeholder={t("targetField")}
              className="w-32 rounded-md border border-zinc-300 px-2 py-1 text-xs dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
            <input
              value={row.transform}
              onChange={(e) => setRows(rows.map((r, idx) => (idx === i ? { ...r, transform: e.target.value } : r)))}
              placeholder={t("transformPlaceholder")}
              className="min-w-0 flex-1 rounded-md border border-zinc-300 px-2 py-1 text-xs font-mono dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
            <button
              type="button"
              data-permission-public="true"
              onClick={() => setRows(rows.filter((_, idx) => idx !== i))}
              className="rounded-md border border-zinc-300 px-2 py-1 text-xs text-black dark:border-zinc-700 dark:text-zinc-50"
            >
              {t("removeFieldMapping")}
            </button>
          </div>
        ))}
        <button
          type="button"
          data-permission-public="true"
          onClick={() => setRows([...rows, emptyFieldMappingRow()])}
          className="self-start rounded-md border border-zinc-300 px-2 py-1 text-xs text-black dark:border-zinc-700 dark:text-zinc-50"
        >
          {t("addFieldMapping")}
        </button>
      </div>
    );
  }

  const connectorName = (connectorId: string) => connectors.find((c) => c.id === connectorId)?.name ?? connectorId;

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      <div className="flex flex-wrap gap-2">
        {(["connectors", "mappings", "syncLogs"] as Section[]).map((s) => (
          <button
            key={s}
            type="button"
            data-permission-public="true"
            aria-pressed={section === s}
            onClick={() => switchSection(s)}
            className={`rounded-md px-3 py-1.5 text-sm font-medium ${
              section === s
                ? "bg-black text-white dark:bg-zinc-50 dark:text-black"
                : "border border-zinc-300 text-black dark:border-zinc-700 dark:text-zinc-50"
            }`}
          >
            {t(s === "connectors" ? "tabConnectors" : s === "mappings" ? "tabMappings" : "tabSyncLogs")}
          </button>
        ))}
      </div>

      {section === "connectors" && (
        <div className="flex flex-col gap-6">
          {isOwner && (
            <form
              onSubmit={submitCreateConnector}
              data-permission-public="true"
              className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
            >
              <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newConnectorTitle")}</h2>
              <label className="flex flex-col gap-1 text-sm">
                {t("name")}
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                {t("type")}
                <select
                  value={type}
                  onChange={(e) => setType(e.target.value as ConnectorType)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                >
                  {CONNECTOR_TYPES.map((ct) => (
                    <option key={ct} value={ct}>
                      {t(`typeName.${ct}`)}
                    </option>
                  ))}
                </select>
              </label>

              {configEditor(configRows, setConfigRows, t("sensitiveFieldHintCreate"))}

              {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
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

          {connectors.length === 0 ? (
            <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
          ) : (
            <div className="flex flex-col gap-3">
              {connectors.map((connector) => (
                <div key={connector.id} className="rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
                  {editingConnectorId === connector.id ? (
                    <form
                      onSubmit={(e) => submitEditConnector(e, connector)}
                      data-permission-public="true"
                      className="flex flex-col gap-3"
                    >
                      <label className="flex flex-col gap-1 text-sm">
                        {t("name")}
                        <input
                          value={editName}
                          onChange={(e) => setEditName(e.target.value)}
                          className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        />
                      </label>
                      <label className="flex flex-col gap-1 text-sm">
                        {t("type")}
                        <select
                          value={editType}
                          onChange={(e) => setEditType(e.target.value as ConnectorType)}
                          className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        >
                          {CONNECTOR_TYPES.map((ct) => (
                            <option key={ct} value={ct}>
                              {t(`typeName.${ct}`)}
                            </option>
                          ))}
                        </select>
                      </label>

                      {configEditor(editConfigRows, setEditConfigRows, t("sensitiveFieldHintEdit"))}

                      <label className="flex items-center gap-2 text-sm">
                        <input type="checkbox" checked={editActive} onChange={(e) => setEditActive(e.target.checked)} />
                        {t("active")}
                      </label>
                      {editError && <p className="text-sm text-red-600 dark:text-red-400">{editError}</p>}
                      <div className="flex gap-2">
                        <button
                          type="submit"
                          data-permission-public="true"
                          disabled={savingConnectorId === connector.id}
                          className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
                        >
                          {t("save")}
                        </button>
                        <button
                          type="button"
                          data-permission-public="true"
                          onClick={cancelEditConnector}
                          className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                        >
                          {t("cancel")}
                        </button>
                      </div>
                    </form>
                  ) : (
                    <div className="flex flex-col gap-2">
                      <div className="flex items-start justify-between gap-4">
                        <div className="flex flex-col gap-1">
                          <span className="font-semibold text-black dark:text-zinc-50">{connector.name}</span>
                          <span className="text-xs text-zinc-600 dark:text-zinc-400">{t(`typeName.${connector.type}`)}</span>
                          <span className="text-xs text-zinc-500 dark:text-zinc-500">
                            {t("status")}: {connector.isActive ? t("active") : t("disabled")} · {t("lastSync")}:{" "}
                            {connector.lastSyncAt ? new Date(connector.lastSyncAt).toLocaleString() : t("never")}
                            {connector.lastSyncStatus ? ` (${connector.lastSyncStatus})` : ""}
                          </span>
                          <div className="flex flex-wrap gap-2 text-xs text-zinc-600 dark:text-zinc-400">
                            {Object.entries(connector.config).map(([k, v]) => (
                              <code key={k} className="rounded-md bg-zinc-100 px-2 py-0.5 font-mono dark:bg-zinc-900">
                                {k}={String(v)}
                              </code>
                            ))}
                          </div>
                        </div>
                        <div className="flex shrink-0 flex-col gap-1">
                          {isOwner && (
                            <>
                              <button
                                type="button"
                                data-permission-public="true"
                                onClick={() => startEditConnector(connector)}
                                className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                              >
                                {t("edit")}
                              </button>
                              <button
                                type="button"
                                data-permission-public="true"
                                disabled={pendingDeleteConnectorId === connector.id}
                                onClick={() => handleDeleteConnector(connector.id)}
                                className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
                              >
                                {t("delete")}
                              </button>
                              <button
                                type="button"
                                data-permission-public="true"
                                disabled={testingConnectorId === connector.id}
                                onClick={() => handleTestConnector(connector.id)}
                                className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
                              >
                                {t("testConnection")}
                              </button>
                            </>
                          )}
                        </div>
                      </div>
                      {connectorRowError?.connectorId === connector.id && (
                        <p className="text-xs text-red-600 dark:text-red-400">{connectorRowError.message}</p>
                      )}
                      {testResult?.connectorId === connector.id && (
                        <p className={`text-xs ${testResult.ok ? "text-emerald-600 dark:text-emerald-400" : "text-red-600 dark:text-red-400"}`}>
                          {testResult.message}
                        </p>
                      )}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {section === "mappings" && (
        <div className="flex flex-col gap-6">
          <label className="flex flex-col gap-1 text-sm">
            {t("connectorLabel")}
            <select
              value={selectedConnectorId}
              onChange={(e) => selectConnectorForMappings(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            >
              <option value="">{t("selectConnectorPrompt")}</option>
              {connectors.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>

          {!selectedConnectorId ? (
            <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("selectConnectorFirst")}</p>
          ) : (
            <>
              {isOwner && (
                <form
                  onSubmit={submitCreateMapping}
                  data-permission-public="true"
                  className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
                >
                  <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newMappingTitle")}</h2>
                  <label className="flex flex-col gap-1 text-sm">
                    {t("sourceEntity")}
                    <input
                      value={sourceEntity}
                      onChange={(e) => setSourceEntity(e.target.value)}
                      className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                    />
                  </label>
                  <label className="flex flex-col gap-1 text-sm">
                    {t("targetEntity")}
                    <input
                      value={targetEntity}
                      onChange={(e) => setTargetEntity(e.target.value)}
                      className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                    />
                  </label>
                  <label className="flex flex-col gap-1 text-sm">
                    {t("syncDirection")}
                    <select
                      value={syncDirection}
                      onChange={(e) => setSyncDirection(e.target.value as SyncDirection)}
                      className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                    >
                      {SYNC_DIRECTIONS.map((d) => (
                        <option key={d} value={d}>
                          {t(`syncDirectionName.${d}`)}
                        </option>
                      ))}
                    </select>
                  </label>

                  {fieldMappingEditor(fieldMappingRows, setFieldMappingRows)}

                  {mappingCreateError && <p className="text-sm text-red-600 dark:text-red-400">{mappingCreateError}</p>}
                  <button
                    type="submit"
                    data-permission-public="true"
                    disabled={submittingMapping}
                    className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
                  >
                    {t("create")}
                  </button>
                </form>
              )}

              {loadingMappings ? null : mappingsError ? (
                <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
              ) : !mappings || mappings.length === 0 ? (
                <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("mappingEmpty")}</p>
              ) : (
                <div className="flex flex-col gap-3">
                  {mappings.map((mapping) => (
                    <div key={mapping.id} className="rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
                      {editingMappingId === mapping.id ? (
                        <form
                          onSubmit={(e) => submitEditMapping(e, mapping)}
                          data-permission-public="true"
                          className="flex flex-col gap-3"
                        >
                          <label className="flex flex-col gap-1 text-sm">
                            {t("sourceEntity")}
                            <input
                              value={editSourceEntity}
                              onChange={(e) => setEditSourceEntity(e.target.value)}
                              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                            />
                          </label>
                          <label className="flex flex-col gap-1 text-sm">
                            {t("targetEntity")}
                            <input
                              value={editTargetEntity}
                              onChange={(e) => setEditTargetEntity(e.target.value)}
                              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                            />
                          </label>
                          <label className="flex flex-col gap-1 text-sm">
                            {t("syncDirection")}
                            <select
                              value={editSyncDirection}
                              onChange={(e) => setEditSyncDirection(e.target.value as SyncDirection)}
                              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                            >
                              {SYNC_DIRECTIONS.map((d) => (
                                <option key={d} value={d}>
                                  {t(`syncDirectionName.${d}`)}
                                </option>
                              ))}
                            </select>
                          </label>

                          {fieldMappingEditor(editFieldMappingRows, setEditFieldMappingRows)}

                          <label className="flex items-center gap-2 text-sm">
                            <input
                              type="checkbox"
                              checked={editMappingActive}
                              onChange={(e) => setEditMappingActive(e.target.checked)}
                            />
                            {t("active")}
                          </label>
                          {mappingEditError && <p className="text-sm text-red-600 dark:text-red-400">{mappingEditError}</p>}
                          <div className="flex gap-2">
                            <button
                              type="submit"
                              data-permission-public="true"
                              disabled={savingMappingId === mapping.id}
                              className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
                            >
                              {t("save")}
                            </button>
                            <button
                              type="button"
                              data-permission-public="true"
                              onClick={cancelEditMapping}
                              className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                            >
                              {t("cancel")}
                            </button>
                          </div>
                        </form>
                      ) : (
                        <div className="flex flex-col gap-2">
                          <div className="flex items-start justify-between gap-4">
                            <div className="flex flex-col gap-1">
                              <span className="font-semibold text-black dark:text-zinc-50">
                                {mapping.sourceEntity} → {mapping.targetEntity}
                              </span>
                              <span className="text-xs text-zinc-600 dark:text-zinc-400">
                                {t(`syncDirectionName.${mapping.syncDirection}`)} ·{" "}
                                {mapping.isActive ? t("active") : t("disabled")}
                              </span>
                              <div className="flex flex-col gap-0.5 text-xs text-zinc-500 dark:text-zinc-500">
                                {mapping.fieldMappings.map((fm, i) => (
                                  <code key={i} className="font-mono">
                                    {fm.source_field} → {fm.target_field}
                                    {fm.transform
                                      ? ` (${Array.isArray(fm.transform) ? fm.transform.join(",") : fm.transform})`
                                      : ""}
                                  </code>
                                ))}
                              </div>
                            </div>
                            {isOwner && (
                              <div className="flex shrink-0 flex-col gap-1">
                                <button
                                  type="button"
                                  data-permission-public="true"
                                  onClick={() => startEditMapping(mapping)}
                                  className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                                >
                                  {t("edit")}
                                </button>
                                <button
                                  type="button"
                                  data-permission-public="true"
                                  disabled={pendingDeleteMappingId === mapping.id}
                                  onClick={() => handleDeleteMapping(mapping.id)}
                                  className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
                                >
                                  {t("delete")}
                                </button>
                              </div>
                            )}
                          </div>
                          {mappingRowError?.mappingId === mapping.id && (
                            <p className="text-xs text-red-600 dark:text-red-400">{mappingRowError.message}</p>
                          )}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      )}

      {section === "syncLogs" && (
        <div className="flex flex-col gap-4">
          <label className="flex flex-col gap-1 text-sm">
            {t("connectorLabel")}
            <select
              value={syncLogConnectorId}
              onChange={(e) => {
                setSyncLogConnectorId(e.target.value);
                void loadSyncLogs(e.target.value);
              }}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            >
              <option value="">{t("allConnectors")}</option>
              {connectors.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>

          {loadingSyncLogs ? null : syncLogsError ? (
            <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
          ) : !syncLogs || syncLogs.length === 0 ? (
            <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("syncLogsEmpty")}</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-xs">
                <thead>
                  <tr className="border-b border-zinc-200 text-zinc-600 dark:border-zinc-800 dark:text-zinc-400">
                    <th className="py-1 pr-3 font-medium">{t("connectorLabel")}</th>
                    <th className="py-1 pr-3 font-medium">{t("startedAt")}</th>
                    <th className="py-1 pr-3 font-medium">{t("finishedAt")}</th>
                    <th className="py-1 pr-3 font-medium">{t("recordsProcessed")}</th>
                    <th className="py-1 pr-3 font-medium">{t("recordsFailed")}</th>
                    <th className="py-1 pr-3 font-medium">{t("statusColumn")}</th>
                    <th className="py-1 font-medium">{t("errorLog")}</th>
                  </tr>
                </thead>
                <tbody>
                  {syncLogs.map((log) => (
                    <tr key={log.id} className="border-b border-zinc-100 text-black dark:border-zinc-900 dark:text-zinc-50">
                      <td className="py-1 pr-3">{connectorName(log.connectorId)}</td>
                      <td className="py-1 pr-3">{new Date(log.startedAt).toLocaleString()}</td>
                      <td className="py-1 pr-3">{log.finishedAt ? new Date(log.finishedAt).toLocaleString() : t("never")}</td>
                      <td className="py-1 pr-3">{log.recordsProcessed}</td>
                      <td className="py-1 pr-3">{log.recordsFailed}</td>
                      <td className="py-1 pr-3">{t(`syncStatusName.${log.status}`)}</td>
                      <td className="py-1 font-mono">{log.errorLog && log.errorLog.length > 0 ? log.errorLog.join("; ") : ""}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
