"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { FirmGroup, FirmGroupRole, IntercompanyTransaction } from "@/lib/consolidation";
import type { PlatformAdminFirm } from "@/lib/platformAdmin";

type Props = {
  groups: FirmGroup[];
  firms: PlatformAdminFirm[];
  locale: string;
};

const ROLES: FirmGroupRole[] = ["parent", "subsidiary", "affiliate"];

// The /platform-admin/firm-groups page's group list plus a create-group
// form, an add-member form, and an inter-company transfer form - one
// group "expanded" (activeGroupId) at a time to keep the two per-group
// forms from cluttering every row. Platform-admin-gated on the backend
// (internal/consolidation.CreateFirmGroup/AddGroupMember/
// PostIntercompanyTransfer); like ExchangeRatesManager.tsx, these forms
// carry data-permission-public="true" rather than a
// data-permission-key, since there is no per-firm permission-catalog key
// for a platform-wide, allowlist-gated action - same reasoning the
// platform-admin page itself is exempt from Permission Audit Mode's
// tagging convention.
export default function FirmGroupsManager({ groups, firms, locale }: Props) {
  const t = useTranslations("FirmGroups");
  const router = useRouter();

  const [activeGroupId, setActiveGroupId] = useState<string | null>(null);

  const [groupName, setGroupName] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const [memberFirmId, setMemberFirmId] = useState("");
  const [memberRole, setMemberRole] = useState<FirmGroupRole>("subsidiary");
  const [memberOwnership, setMemberOwnership] = useState("");
  const [memberError, setMemberError] = useState<string | null>(null);
  const [addingMember, setAddingMember] = useState(false);

  const [fromFirmId, setFromFirmId] = useState("");
  const [toFirmId, setToFirmId] = useState("");
  const [amount, setAmount] = useState("");
  const [currency, setCurrency] = useState("");
  const [description, setDescription] = useState("");
  const [transferError, setTransferError] = useState<string | null>(null);
  const [transferResult, setTransferResult] = useState<IntercompanyTransaction | null>(null);
  const [transferring, setTransferring] = useState(false);

  function resetMemberForm() {
    setMemberFirmId("");
    setMemberRole("subsidiary");
    setMemberOwnership("");
    setMemberError(null);
  }

  function resetTransferForm() {
    setFromFirmId("");
    setToFirmId("");
    setAmount("");
    setCurrency("");
    setDescription("");
    setTransferError(null);
    setTransferResult(null);
  }

  function toggleGroup(groupId: string) {
    const next = activeGroupId === groupId ? null : groupId;
    setActiveGroupId(next);
    resetMemberForm();
    resetTransferForm();
  }

  async function submitCreateGroup(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!groupName.trim()) {
      setCreateError(t("createGroupRequired"));
      return;
    }
    setCreating(true);
    try {
      const res = await fetch(`/api/platform-admin/firm-groups`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: groupName.trim() }),
      });
      if (!res.ok) {
        setCreateError(t("createGroupError"));
        return;
      }
      setGroupName("");
      router.refresh();
    } catch {
      setCreateError(t("createGroupError"));
    } finally {
      setCreating(false);
    }
  }

  async function submitAddMember(e: FormEvent, groupId: string) {
    e.preventDefault();
    setMemberError(null);
    if (!memberFirmId) {
      setMemberError(t("addMemberRequired"));
      return;
    }
    setAddingMember(true);
    try {
      const res = await fetch(`/api/platform-admin/firm-groups/${groupId}/members`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          firmId: memberFirmId,
          role: memberRole,
          ownershipPct: memberOwnership.trim() || undefined,
        }),
      });
      if (!res.ok) {
        setMemberError(t("addMemberError"));
        return;
      }
      resetMemberForm();
      router.refresh();
    } catch {
      setMemberError(t("addMemberError"));
    } finally {
      setAddingMember(false);
    }
  }

  async function submitTransfer(e: FormEvent, groupId: string) {
    e.preventDefault();
    setTransferError(null);
    setTransferResult(null);
    if (!fromFirmId || !toFirmId || !amount.trim() || !currency.trim() || !description.trim()) {
      setTransferError(t("transferRequired"));
      return;
    }
    setTransferring(true);
    try {
      const res = await fetch(`/api/platform-admin/firm-groups/${groupId}/intercompany-transfer`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          fromFirmId,
          toFirmId,
          amount: amount.trim(),
          currency: currency.trim().toUpperCase(),
          description: description.trim(),
        }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { error?: string };
        setTransferError(body.error || t("transferError"));
        return;
      }
      const txn = (await res.json()) as IntercompanyTransaction;
      setTransferResult(txn);
      setAmount("");
      setDescription("");
      router.refresh();
    } catch {
      setTransferError(t("transferError"));
    } finally {
      setTransferring(false);
    }
  }

  return (
    <div className="flex w-full flex-col gap-6">
      <form
        onSubmit={submitCreateGroup}
        data-permission-public="true"
        className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
      >
        <h2 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("newGroupTitle")}
        </h2>
        <div className="flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-1 text-sm">
            {t("groupNameLabel")}
            <input
              value={groupName}
              onChange={(e) => setGroupName(e.target.value)}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <button
            type="submit"
            data-permission-public="true"
            disabled={creating}
            className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
          >
            {t("createGroup")}
          </button>
        </div>
        {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
      </form>

      {groups.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="flex flex-col gap-4">
          {groups.map((group) => (
            <div key={group.id} className="rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h3 className="text-base font-semibold text-black dark:text-zinc-50">{group.name}</h3>
                  <p className="text-xs text-zinc-500 dark:text-zinc-400">
                    {t("createdAtLabel")} {new Date(group.createdAt).toLocaleString()}
                  </p>
                </div>
                <div className="flex gap-2">
                  <a
                    href={`/${locale}/platform-admin/firm-groups/${group.id}/pnl`}
                    data-permission-public="true"
                    className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm text-zinc-700 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-900"
                  >
                    {t("viewPnL")}
                  </a>
                  <button
                    type="button"
                    data-permission-public="true"
                    onClick={() => toggleGroup(group.id)}
                    className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm text-zinc-700 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-900"
                  >
                    {activeGroupId === group.id ? t("manageClose") : t("manageOpen")}
                  </button>
                </div>
              </div>

              {group.members.length === 0 ? (
                <p className="mt-3 text-sm text-zinc-500 dark:text-zinc-500">{t("noMembers")}</p>
              ) : (
                <div className="mt-3 overflow-x-auto">
                  <table className="w-full border-collapse text-left text-sm">
                    <thead>
                      <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                        <th className="py-1.5 pr-4 font-medium">{t("columnFirm")}</th>
                        <th className="py-1.5 pr-4 font-medium">{t("columnRole")}</th>
                        <th className="py-1.5 font-medium">{t("columnOwnership")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {group.members.map((m) => (
                        <tr
                          key={m.firmId}
                          className="border-b border-zinc-200 text-black last:border-0 dark:border-zinc-800 dark:text-zinc-50"
                        >
                          <td className="py-1.5 pr-4">{m.firmName}</td>
                          <td className="py-1.5 pr-4">{t(`role.${m.role}`)}</td>
                          <td className="py-1.5">{m.ownershipPct ? `${m.ownershipPct}%` : "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}

              {activeGroupId === group.id && (
                <div className="mt-4 flex flex-col gap-4 border-t border-zinc-200 pt-4 dark:border-zinc-800">
                  <form
                    onSubmit={(e) => submitAddMember(e, group.id)}
                    data-permission-public="true"
                    className="flex flex-col gap-3"
                  >
                    <h4 className="text-sm font-semibold text-black dark:text-zinc-50">{t("addMemberTitle")}</h4>
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                      <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                        {t("firmLabel")}
                        <select
                          value={memberFirmId}
                          onChange={(e) => setMemberFirmId(e.target.value)}
                          className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        >
                          <option value="">{t("selectFirm")}</option>
                          {firms.map((f) => (
                            <option key={f.firmId} value={f.firmId}>
                              {f.name}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                        {t("roleLabel")}
                        <select
                          value={memberRole}
                          onChange={(e) => setMemberRole(e.target.value as FirmGroupRole)}
                          className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        >
                          {ROLES.map((role) => (
                            <option key={role} value={role}>
                              {t(`role.${role}`)}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                        {t("ownershipLabel")}
                        <input
                          value={memberOwnership}
                          onChange={(e) => setMemberOwnership(e.target.value)}
                          placeholder="80.00" // i18n-ignore: numeric example value, not translatable text
                          className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        />
                      </label>
                    </div>
                    {memberError && <p className="text-sm text-red-600 dark:text-red-400">{memberError}</p>}
                    <button
                      type="submit"
                      data-permission-public="true"
                      disabled={addingMember}
                      className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
                    >
                      {t("addMember")}
                    </button>
                  </form>

                  <form
                    onSubmit={(e) => submitTransfer(e, group.id)}
                    data-permission-public="true"
                    className="flex flex-col gap-3"
                  >
                    <h4 className="text-sm font-semibold text-black dark:text-zinc-50">{t("transferTitle")}</h4>
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                        {t("fromFirmLabel")}
                        <select
                          value={fromFirmId}
                          onChange={(e) => setFromFirmId(e.target.value)}
                          className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        >
                          <option value="">{t("selectFirm")}</option>
                          {group.members.map((m) => (
                            <option key={m.firmId} value={m.firmId}>
                              {m.firmName}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                        {t("toFirmLabel")}
                        <select
                          value={toFirmId}
                          onChange={(e) => setToFirmId(e.target.value)}
                          className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        >
                          <option value="">{t("selectFirm")}</option>
                          {group.members.map((m) => (
                            <option key={m.firmId} value={m.firmId}>
                              {m.firmName}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                        {t("amountLabel")}
                        <input
                          value={amount}
                          onChange={(e) => setAmount(e.target.value)}
                          placeholder="500.00" // i18n-ignore: numeric example value, not translatable text
                          className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        />
                      </label>
                      <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
                        {t("currencyLabel")}
                        <input
                          value={currency}
                          onChange={(e) => setCurrency(e.target.value)}
                          placeholder="USD" // i18n-ignore: ISO 4217 currency code example, not translatable text
                          className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm uppercase dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        />
                      </label>
                      <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400 sm:col-span-2">
                        {t("descriptionLabel")}
                        <input
                          value={description}
                          onChange={(e) => setDescription(e.target.value)}
                          className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                        />
                      </label>
                    </div>
                    {transferError && <p className="text-sm text-red-600 dark:text-red-400">{transferError}</p>}
                    {transferResult && (
                      <p className="text-sm text-emerald-600 dark:text-emerald-400">
                        {t("transferSuccess", {
                          fromEntry: transferResult.fromJournalEntryId ?? "—",
                          toEntry: transferResult.toJournalEntryId ?? "—",
                        })}
                      </p>
                    )}
                    <button
                      type="submit"
                      data-permission-public="true"
                      disabled={transferring}
                      className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
                    >
                      {t("postTransfer")}
                    </button>
                  </form>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
