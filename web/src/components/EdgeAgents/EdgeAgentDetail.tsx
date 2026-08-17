"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { CommandStatus, EdgeAgent, EdgeAgentToken, EdgeCommand, EdgeEvent } from "@/lib/edgeAgents";

type Props = {
  firmId: string;
  agent: EdgeAgent;
  tokens: EdgeAgentToken[];
  events: EdgeEvent[];
  commands: EdgeCommand[];
  isOwner: boolean;
};

// Command status badge colors - mirrors AssetDetail.tsx's own
// statusBadgeClass convention (a switch over the closed status
// vocabulary, one Tailwind light/dark pair per case).
function commandStatusBadgeClass(status: CommandStatus): string {
  switch (status) {
    case "pending":
      return "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-400";
    case "delivered":
      return "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-400";
    case "acknowledged":
      return "bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-400";
    case "failed":
      return "bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-400";
  }
}

// The /edge-agents/{agentId} detail page: the agent's own status/
// capabilities, its event log (most recent first, from
// internal/edgeagent.ListEvents), and - owner-only - token management.
// Issuing a token shows the plaintext exactly once, right after
// internal/edgeagent.IssueToken returns it (see that function's own doc
// comment on why ZonaryOS never has it again after this); every token
// listed below that is never shows the secret, only its name/expiry/
// last-used metadata.
export default function EdgeAgentDetail({ firmId, agent, tokens, events, commands, isOwner }: Props) {
  const t = useTranslations("EdgeAgents");
  const router = useRouter();

  const [issuing, setIssuing] = useState(false);
  const [tokenName, setTokenName] = useState("");
  const [issueError, setIssueError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [justIssued, setJustIssued] = useState<string | null>(null);

  const [sendingCommand, setSendingCommand] = useState(false);
  const [commandType, setCommandType] = useState("");
  const [commandPayloadText, setCommandPayloadText] = useState("");
  const [commandError, setCommandError] = useState<string | null>(null);
  const [commandSubmitting, setCommandSubmitting] = useState(false);
  const [commandSuccess, setCommandSuccess] = useState(false);

  async function submitIssueToken(e: FormEvent) {
    e.preventDefault();
    setIssueError(null);
    const trimmedName = tokenName.trim();
    if (!trimmedName) {
      setIssueError(t("issueTokenNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/edge-agents/${firmId}/${agent.id}/tokens`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: trimmedName }),
      });
      if (!res.ok) {
        setIssueError(t("issueTokenError"));
        return;
      }
      const data = (await res.json()) as { token: string };
      setJustIssued(data.token);
      setIssuing(false);
      setTokenName("");
      router.refresh();
    } catch {
      setIssueError(t("issueTokenError"));
    } finally {
      setSubmitting(false);
    }
  }

  // Freeform JSON textarea for the command payload, not structured fields:
  // unlike RuleBuilder's action rows (a fixed, schema-known set of action
  // types), a command's payload shape is entirely up to commandType, which
  // is itself a free-text string the agent-side code defines - there is no
  // firm-side schema to build a structured form against. An empty textarea
  // sends no payload at all (backend defaults it to `{}`, see
  // CreateCommand's own doc comment), same "empty means omit" convention
  // the token-name field above uses for its own optional fields.
  async function submitCommand(e: FormEvent) {
    e.preventDefault();
    setCommandError(null);
    setCommandSuccess(false);
    const trimmedType = commandType.trim();
    if (!trimmedType) {
      setCommandError(t("commandTypeRequired"));
      return;
    }

    let payload: Record<string, unknown> | undefined;
    const trimmedPayload = commandPayloadText.trim();
    if (trimmedPayload) {
      try {
        const parsed = JSON.parse(trimmedPayload);
        if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
          setCommandError(t("commandPayloadInvalid"));
          return;
        }
        payload = parsed as Record<string, unknown>;
      } catch {
        setCommandError(t("commandPayloadInvalid"));
        return;
      }
    }

    setCommandSubmitting(true);
    try {
      const res = await fetch(`/api/edge-agents/${firmId}/${agent.id}/commands`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ commandType: trimmedType, payload }),
      });
      if (!res.ok) {
        setCommandError(t("commandSendError"));
        return;
      }
      setCommandSuccess(true);
      setSendingCommand(false);
      setCommandType("");
      setCommandPayloadText("");
      router.refresh();
    } catch {
      setCommandError(t("commandSendError"));
    } finally {
      setCommandSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-8">
      <div className="flex flex-col gap-1">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{agent.name}</h1>
        {agent.description && <p className="text-sm text-zinc-600 dark:text-zinc-400">{agent.description}</p>}
        <dl className="mt-2 grid grid-cols-2 gap-x-6 gap-y-1 text-sm sm:grid-cols-4">
          <div>
            <dt className="text-zinc-500 dark:text-zinc-500">{t("status.label")}</dt>
            <dd className="text-black dark:text-zinc-50">{t(`status.${agent.status}`)}</dd>
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-500">{t("lastSeen")}</dt>
            <dd className="text-black dark:text-zinc-50">
              {agent.lastSeenAt ? new Date(agent.lastSeenAt).toLocaleString() : t("neverSeen")}
            </dd>
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-500">{t("version")}</dt>
            <dd className="text-black dark:text-zinc-50">{agent.version ?? "—"}</dd>
          </div>
          <div>
            <dt className="text-zinc-500 dark:text-zinc-500">{t("capabilities")}</dt>
            <dd className="text-black dark:text-zinc-50">
              {agent.capabilities.length > 0 ? agent.capabilities.join(", ") : "—"}
            </dd>
          </div>
        </dl>
      </div>

      {isOwner && (
        <div className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("tokensTitle")}</h2>
            <button
              type="button"
              data-permission-public="true"
              onClick={() => {
                setJustIssued(null);
                setIssuing((v) => !v);
              }}
              className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            >
              {issuing ? t("cancel") : t("issueToken")}
            </button>
          </div>

          {justIssued && (
            <div className="flex flex-col gap-1 rounded-md border border-amber-400 bg-amber-50 p-3 text-sm dark:border-amber-700 dark:bg-amber-950">
              <p className="font-medium text-amber-900 dark:text-amber-200">{t("issuedTokenWarning")}</p>
              <code className="break-all rounded bg-white px-2 py-1 text-xs text-black dark:bg-black dark:text-zinc-50">
                {justIssued}
              </code>
            </div>
          )}

          {issuing && (
            <form onSubmit={submitIssueToken} data-permission-public="true" className="flex flex-col gap-3">
              <label className="flex flex-col gap-1 text-sm">
                {t("tokenName")}
                <input
                  value={tokenName}
                  onChange={(e) => setTokenName(e.target.value)}
                  className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
              </label>
              {issueError && <p className="text-sm text-red-600 dark:text-red-400">{issueError}</p>}
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

          {tokens.length === 0 ? (
            <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("noTokens")}</p>
          ) : (
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="text-zinc-500 dark:text-zinc-500">
                  <th className="pb-1 font-normal">{t("tokenName")}</th>
                  <th className="pb-1 font-normal">{t("lastUsed")}</th>
                  <th className="pb-1 font-normal">{t("expires")}</th>
                </tr>
              </thead>
              <tbody>
                {tokens.map((tok) => (
                  <tr key={tok.id} className="border-t border-zinc-200 dark:border-zinc-800">
                    <td className="py-1.5 text-black dark:text-zinc-50">{tok.name}</td>
                    <td className="py-1.5 text-zinc-600 dark:text-zinc-400">
                      {tok.lastUsedAt ? new Date(tok.lastUsedAt).toLocaleString() : t("neverSeen")}
                    </td>
                    <td className="py-1.5 text-zinc-600 dark:text-zinc-400">
                      {tok.expiresAt ? new Date(tok.expiresAt).toLocaleString() : "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      <div className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("commandsTitle")}
          </h2>
          {isOwner && (
            <button
              type="button"
              data-permission-public="true"
              onClick={() => {
                setCommandSuccess(false);
                setSendingCommand((v) => !v);
              }}
              className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            >
              {sendingCommand ? t("cancel") : t("sendCommand")}
            </button>
          )}
        </div>

        {commandSuccess && (
          <p className="text-sm text-green-700 dark:text-green-400">{t("commandSendSuccess")}</p>
        )}

        {isOwner && sendingCommand && (
          <form onSubmit={submitCommand} data-permission-public="true" className="flex flex-col gap-3">
            <label className="flex flex-col gap-1 text-sm">
              {t("commandType")}
              <input
                value={commandType}
                onChange={(e) => setCommandType(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("commandPayload")}
              <textarea
                value={commandPayloadText}
                onChange={(e) => setCommandPayloadText(e.target.value)}
                rows={4}
                placeholder={t("commandPayloadPlaceholder")}
                className="rounded-md border border-zinc-300 px-2 py-1.5 font-mono text-xs dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
              <span className="text-xs text-zinc-500 dark:text-zinc-500">{t("commandPayloadHint")}</span>
            </label>
            {commandError && <p className="text-sm text-red-600 dark:text-red-400">{commandError}</p>}
            <button
              type="submit"
              data-permission-public="true"
              disabled={commandSubmitting}
              className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
            >
              {t("send")}
            </button>
          </form>
        )}

        {commands.length === 0 ? (
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("noCommands")}</p>
        ) : (
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="text-zinc-500 dark:text-zinc-500">
                <th className="pb-1 font-normal">{t("commandType")}</th>
                <th className="pb-1 font-normal">{t("status.label")}</th>
                <th className="pb-1 font-normal">{t("commandPayload")}</th>
                <th className="pb-1 font-normal">{t("commandCreatedAt")}</th>
                <th className="pb-1 font-normal">{t("commandDeliveredAt")}</th>
                <th className="pb-1 font-normal">{t("commandAcknowledgedAt")}</th>
              </tr>
            </thead>
            <tbody>
              {commands.map((cmd) => (
                <tr key={cmd.id} className="border-t border-zinc-200 dark:border-zinc-800">
                  <td className="py-1.5 text-black dark:text-zinc-50">{cmd.commandType}</td>
                  <td className="py-1.5">
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs font-medium ${commandStatusBadgeClass(cmd.status)}`}
                    >
                      {t(`commandStatus.${cmd.status}`)}
                    </span>
                  </td>
                  <td className="max-w-xs truncate py-1.5 font-mono text-xs text-zinc-600 dark:text-zinc-400">
                    {Object.keys(cmd.payload).length > 0 ? JSON.stringify(cmd.payload) : "—"}
                  </td>
                  <td className="py-1.5 text-zinc-600 dark:text-zinc-400">
                    {new Date(cmd.createdAt).toLocaleString()}
                  </td>
                  <td className="py-1.5 text-zinc-600 dark:text-zinc-400">
                    {cmd.deliveredAt ? new Date(cmd.deliveredAt).toLocaleString() : "—"}
                  </td>
                  <td className="py-1.5 text-zinc-600 dark:text-zinc-400">
                    {cmd.acknowledgedAt ? new Date(cmd.acknowledgedAt).toLocaleString() : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="flex flex-col gap-3">
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("eventsTitle")}</h2>
        {events.length === 0 ? (
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("noEvents")}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {events.map((ev) => (
              <li
                key={ev.id}
                className="flex items-center justify-between rounded-md border border-zinc-300 px-4 py-2 text-sm dark:border-zinc-700"
              >
                <span className="font-medium text-black dark:text-zinc-50">{ev.eventType}</span>
                <span className="text-zinc-600 dark:text-zinc-400">{new Date(ev.receivedAt).toLocaleString()}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
