"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { WEBHOOK_EVENTS, type Webhook, type WebhookDelivery, type WebhookInput } from "@/lib/webhooks";

type Props = {
  firmId: string;
  webhooks: Webhook[];
};

// The /settings/webhooks page's client half: a create form (name, URL,
// event checkboxes drawn from WEBHOOK_EVENTS), per-row edit (inline,
// same form shape reused), delete, and an expandable delivery-history
// panel per row - mirrors components/Settings/ApiKeysManager.tsx's own
// shape. Unlike ApiKeysManager's one-time secret reveal, a webhook's
// secret is shown persistently in its row (see lib/webhooks.ts's own doc
// comment on why it's stored/returned plaintext).
export default function WebhooksManager({ firmId, webhooks }: Props) {
  const t = useTranslations("WebhooksSettings");
  const router = useRouter();

  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [selectedEvents, setSelectedEvents] = useState<string[]>([]);
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editUrl, setEditUrl] = useState("");
  const [editEvents, setEditEvents] = useState<string[]>([]);
  const [editActive, setEditActive] = useState(true);
  const [editError, setEditError] = useState<string | null>(null);
  const [savingId, setSavingId] = useState<string | null>(null);

  const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ webhookId: string; message: string } | null>(null);

  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [deliveries, setDeliveries] = useState<Record<string, WebhookDelivery[] | null>>({});
  const [loadingDeliveries, setLoadingDeliveries] = useState<string | null>(null);

  function toggleEvent(events: string[], setEvents: (e: string[]) => void, event: string) {
    setEvents(events.includes(event) ? events.filter((e) => e !== event) : [...events, event]);
  }

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!name.trim()) {
      setCreateError(t("createNameRequired"));
      return;
    }
    if (!url.trim()) {
      setCreateError(t("createUrlRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const input: WebhookInput = { name: name.trim(), url: url.trim(), events: selectedEvents, isActive: true };
      const res = await fetch(`/api/webhooks/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setName("");
      setUrl("");
      setSelectedEvents([]);
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  function startEdit(hook: Webhook) {
    setEditingId(hook.id);
    setEditName(hook.name);
    setEditUrl(hook.url);
    setEditEvents(hook.events);
    setEditActive(hook.isActive);
    setEditError(null);
  }

  function cancelEdit() {
    setEditingId(null);
    setEditError(null);
  }

  async function submitEdit(e: FormEvent, webhookId: string) {
    e.preventDefault();
    setEditError(null);
    if (!editName.trim() || !editUrl.trim()) {
      setEditError(t("createNameRequired"));
      return;
    }

    setSavingId(webhookId);
    try {
      const input: WebhookInput = { name: editName.trim(), url: editUrl.trim(), events: editEvents, isActive: editActive };
      const res = await fetch(`/api/webhooks/${firmId}/${webhookId}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      });
      if (!res.ok) {
        setEditError(t("saveError"));
        return;
      }
      setEditingId(null);
      router.refresh();
    } catch {
      setEditError(t("saveError"));
    } finally {
      setSavingId(null);
    }
  }

  async function handleDelete(webhookId: string) {
    setRowError(null);
    setPendingDeleteId(webhookId);
    try {
      const res = await fetch(`/api/webhooks/${firmId}/${webhookId}`, { method: "DELETE" });
      if (!res.ok) {
        setRowError({ webhookId, message: t("deleteError") });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ webhookId, message: t("deleteError") });
    } finally {
      setPendingDeleteId(null);
    }
  }

  async function toggleDeliveries(webhookId: string) {
    if (expandedId === webhookId) {
      setExpandedId(null);
      return;
    }
    setExpandedId(webhookId);
    if (deliveries[webhookId] !== undefined) return;

    setLoadingDeliveries(webhookId);
    try {
      const res = await fetch(`/api/webhooks/${firmId}/${webhookId}/deliveries`);
      if (!res.ok) {
        setDeliveries((prev) => ({ ...prev, [webhookId]: null }));
        return;
      }
      const data = (await res.json()) as WebhookDelivery[];
      setDeliveries((prev) => ({ ...prev, [webhookId]: data }));
    } catch {
      setDeliveries((prev) => ({ ...prev, [webhookId]: null }));
    } finally {
      setLoadingDeliveries(null);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <form
        onSubmit={submitCreate}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("newWebhookTitle")}</h2>
        <label className="flex flex-col gap-1 text-sm">
          {t("name")}
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>
        <label className="flex flex-col gap-1 text-sm">
          {t("url")}
          <input
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>

        <div className="flex flex-col gap-1">
          <span className="text-sm">{t("events")}</span>
          <div className="grid grid-cols-1 gap-1 sm:grid-cols-2">
            {WEBHOOK_EVENTS.map((event) => (
              <label key={event} className="flex items-center gap-2 text-xs">
                <input
                  type="checkbox"
                  checked={selectedEvents.includes(event)}
                  onChange={() => toggleEvent(selectedEvents, setSelectedEvents, event)}
                />
                <span className="font-mono">{event}</span>
              </label>
            ))}
          </div>
        </div>

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

      {webhooks.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="flex flex-col gap-3">
          {webhooks.map((hook) => (
            <div key={hook.id} className="rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
              {editingId === hook.id ? (
                <form onSubmit={(e) => submitEdit(e, hook.id)} data-permission-public="true" className="flex flex-col gap-3">
                  <label className="flex flex-col gap-1 text-sm">
                    {t("name")}
                    <input
                      value={editName}
                      onChange={(e) => setEditName(e.target.value)}
                      className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                    />
                  </label>
                  <label className="flex flex-col gap-1 text-sm">
                    {t("url")}
                    <input
                      value={editUrl}
                      onChange={(e) => setEditUrl(e.target.value)}
                      className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                    />
                  </label>
                  <div className="flex flex-col gap-1">
                    <span className="text-sm">{t("events")}</span>
                    <div className="grid grid-cols-1 gap-1 sm:grid-cols-2">
                      {WEBHOOK_EVENTS.map((event) => (
                        <label key={event} className="flex items-center gap-2 text-xs">
                          <input
                            type="checkbox"
                            checked={editEvents.includes(event)}
                            onChange={() => toggleEvent(editEvents, setEditEvents, event)}
                          />
                          <span className="font-mono">{event}</span>
                        </label>
                      ))}
                    </div>
                  </div>
                  <label className="flex items-center gap-2 text-sm">
                    <input type="checkbox" checked={editActive} onChange={(e) => setEditActive(e.target.checked)} />
                    {t("active")}
                  </label>
                  {editError && <p className="text-sm text-red-600 dark:text-red-400">{editError}</p>}
                  <div className="flex gap-2">
                    <button
                      type="submit"
                      data-permission-public="true"
                      disabled={savingId === hook.id}
                      className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
                    >
                      {t("save")}
                    </button>
                    <button
                      type="button"
                      data-permission-public="true"
                      onClick={cancelEdit}
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
                      <span className="font-semibold text-black dark:text-zinc-50">{hook.name}</span>
                      <span className="break-all font-mono text-xs text-zinc-600 dark:text-zinc-400">{hook.url}</span>
                      <span className="text-xs text-zinc-600 dark:text-zinc-400">
                        {hook.events.length === 0 ? "" : hook.events.join(", ")}
                      </span>
                      <span className="text-xs text-zinc-500 dark:text-zinc-500">
                        {t("status")}: {hook.isActive ? t("active") : t("disabled")} ·{" "}
                        {t("lastTriggered")}: {hook.lastTriggeredAt ? new Date(hook.lastTriggeredAt).toLocaleString() : t("never")}
                      </span>
                      <code className="break-all rounded-md bg-zinc-100 px-2 py-1 font-mono text-xs text-black dark:bg-zinc-900 dark:text-zinc-50">
                        {hook.secret}
                      </code>
                    </div>
                    <div className="flex shrink-0 flex-col gap-1">
                      <button
                        type="button"
                        data-permission-public="true"
                        onClick={() => startEdit(hook)}
                        className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                      >
                        {t("edit")}
                      </button>
                      <button
                        type="button"
                        data-permission-public="true"
                        disabled={pendingDeleteId === hook.id}
                        onClick={() => handleDelete(hook.id)}
                        className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
                      >
                        {t("delete")}
                      </button>
                      <button
                        type="button"
                        data-permission-public="true"
                        onClick={() => toggleDeliveries(hook.id)}
                        className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
                      >
                        {expandedId === hook.id ? t("hideDeliveries") : t("viewDeliveries")}
                      </button>
                    </div>
                  </div>
                  {rowError?.webhookId === hook.id && (
                    <p className="text-xs text-red-600 dark:text-red-400">{rowError.message}</p>
                  )}

                  {expandedId === hook.id && (
                    <div className="mt-2 rounded-md border border-zinc-200 p-3 dark:border-zinc-800">
                      <h3 className="mb-2 text-sm font-semibold text-black dark:text-zinc-50">{t("deliveriesTitle")}</h3>
                      {loadingDeliveries === hook.id ? null : deliveries[hook.id] == null ? (
                        <p className="text-xs text-zinc-500 dark:text-zinc-400">{t("deliveriesEmpty")}</p>
                      ) : deliveries[hook.id]!.length === 0 ? (
                        <p className="text-xs text-zinc-500 dark:text-zinc-400">{t("deliveriesEmpty")}</p>
                      ) : (
                        <div className="overflow-x-auto">
                          <table className="w-full border-collapse text-left text-xs">
                            <thead>
                              <tr className="border-b border-zinc-200 text-zinc-600 dark:border-zinc-800 dark:text-zinc-400">
                                <th className="py-1 pr-3 font-medium">{t("deliveryEvent")}</th>
                                <th className="py-1 pr-3 font-medium">{t("deliveryStatus")}</th>
                                <th className="py-1 font-medium">{t("deliveryTime")}</th>
                              </tr>
                            </thead>
                            <tbody>
                              {deliveries[hook.id]!.map((d) => (
                                <tr key={d.id} className="border-b border-zinc-100 text-black dark:border-zinc-900 dark:text-zinc-50">
                                  <td className="py-1 pr-3 font-mono">{d.eventType}</td>
                                  <td className="py-1 pr-3">{d.success ? t("deliverySuccess") : t("deliveryFailed")}</td>
                                  <td className="py-1">{new Date(d.deliveredAt).toLocaleString()}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
