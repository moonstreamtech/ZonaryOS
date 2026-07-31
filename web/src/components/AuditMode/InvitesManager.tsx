"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Invite } from "@/lib/invite";
import type { RoleGrant } from "@/lib/permissionAudit";

type Props = {
  firmId: string;
  locale: string;
  invites: Invite[];
  // Non-owner roles only (see internal/permission.GetFirmPermissionAudit's
  // own is_owner exclusion, RoleManager.tsx's identical precedent): a
  // firm's owner role is never offered as an invite target from this UI -
  // structurally, not just cosmetically, since internal/invite.Generate's
  // own roleID validation would still accept it if it were offered here,
  // there is nothing backend-side that specifically blocks inviting into
  // an owner role. This is a deliberate scope choice for this batch, not
  // a security boundary - see docs/DEVELOPMENT.md's Invites section.
  roles: RoleGrant[];
};

// Open Points item 17's companion gap, resolved without email: an owner
// generates a link, copies it out of this panel, and shares it by
// whatever channel they already use. Deliberately modeled after
// RoleManager.tsx's own create-row-then-list shape (same
// creating/submitting/rowError/pendingId state machine) rather than a new
// pattern.
export default function InvitesManager({ firmId, locale, invites, roles }: Props) {
  const t = useTranslations("Members");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [selectedRoleId, setSelectedRoleId] = useState(roles[0]?.roleId ?? "");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [justCreatedLink, setJustCreatedLink] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const [rowError, setRowError] = useState<{ inviteId: string; message: string } | null>(null);
  const [pendingInviteId, setPendingInviteId] = useState<string | null>(null);

  function inviteLink(token: string): string {
    if (typeof window === "undefined") return `/${locale}/invite/${token}`;
    return `${window.location.origin}/${locale}/invite/${token}`;
  }

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!selectedRoleId) {
      setCreateError(t("invitesRoleRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/invites/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ roleId: selectedRoleId }),
      });
      if (!res.ok) {
        setCreateError(t("invitesCreateError"));
        return;
      }
      const invite = (await res.json()) as Invite;
      setJustCreatedLink(invite.token ? inviteLink(invite.token) : null);
      setCopied(false);
      setCreating(false);
      router.refresh();
    } catch {
      setCreateError(t("invitesCreateError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleRevoke(inviteId: string) {
    if (!window.confirm(t("invitesRevokeConfirm"))) return;
    setRowError(null);
    setPendingInviteId(inviteId);
    try {
      const res = await fetch(`/api/invites/${firmId}/${inviteId}/revoke`, { method: "POST" });
      if (!res.ok) {
        setRowError({ inviteId, message: t("invitesRevokeError") });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ inviteId, message: t("invitesRevokeError") });
    } finally {
      setPendingInviteId(null);
    }
  }

  async function copyLink() {
    if (!justCreatedLink) return;
    try {
      await navigator.clipboard.writeText(justCreatedLink);
      setCopied(true);
    } catch {
      // Clipboard permission denied or unavailable (e.g. non-HTTPS
      // context) - the link is still shown as selectable text, so the
      // owner can still copy it manually; this just skips the "Copied"
      // confirmation.
    }
  }

  function statusLabel(invite: Invite): string {
    if (invite.status === "pending" && invite.expired) return t("invitesStatusExpired");
    if (invite.status === "pending") return t("invitesStatusPending");
    if (invite.status === "used") return t("invitesStatusUsed");
    return t("invitesStatusRevoked");
  }

  return (
    <div className="w-full max-w-2xl">
      <div className="mb-2 flex items-center justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold text-black dark:text-zinc-50">
            {t("invitesTitle")}
          </h2>
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("invitesDescription")}</p>
        </div>
        {!creating && roles.length > 0 && (
          <button
            type="button"
            data-permission-public="true"
            onClick={() => {
              setCreating(true);
              setJustCreatedLink(null);
            }}
            className="shrink-0 rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
          >
            {t("invitesCreateButton")}
          </button>
        )}
      </div>

      {roles.length === 0 && (
        <p className="mb-4 text-sm text-zinc-600 dark:text-zinc-400">
          {t("invitesNoRolesHint")}
        </p>
      )}

      {justCreatedLink && (
        <div className="mb-4 flex flex-col gap-2 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950">
          <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("invitesLinkReady")}</p>
          <div className="flex flex-wrap items-center gap-2">
            <code className="flex-1 overflow-x-auto rounded-md bg-zinc-100 px-2 py-1 text-xs text-black dark:bg-zinc-900 dark:text-zinc-50">
              {justCreatedLink}
            </code>
            <button
              type="button"
              onClick={copyLink}
              className="rounded-md border border-zinc-300 px-3 py-1 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
            >
              {copied ? t("invitesLinkCopied") : t("invitesLinkCopy")}
            </button>
          </div>
        </div>
      )}

      {creating && (
        <form
          onSubmit={submitCreate}
          className="mb-4 flex flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950 sm:flex-row sm:items-end"
        >
          <div className="flex-1">
            <label className="mb-1 block text-xs font-medium text-zinc-600 dark:text-zinc-400">
              {t("invitesRoleLabel")}
            </label>
            <select
              value={selectedRoleId}
              onChange={(e) => setSelectedRoleId(e.target.value)}
              className="w-full rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
            >
              {roles.map((role) => (
                <option key={role.roleId} value={role.roleId}>
                  {role.roleName}
                </option>
              ))}
            </select>
          </div>
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={submitting}
              className="rounded-md bg-foreground px-4 py-1.5 text-xs font-medium text-background disabled:opacity-50"
            >
              {t("invitesCreateSubmit")}
            </button>
            <button
              type="button"
              onClick={() => {
                setCreating(false);
                setCreateError(null);
              }}
              className="rounded-md border border-zinc-300 px-4 py-1.5 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
            >
              {t("invitesCreateCancel")}
            </button>
          </div>
          {createError && (
            <p className="w-full text-xs text-red-600 dark:text-red-400">{createError}</p>
          )}
        </form>
      )}

      {invites.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("invitesEmpty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("invitesColumnRole")}</th>
                <th className="py-2 pr-4 font-medium">{t("invitesColumnStatus")}</th>
                <th className="py-2 font-medium">{t("invitesColumnActions")}</th>
              </tr>
            </thead>
            <tbody>
              {invites.map((invite) => (
                <tr
                  key={invite.inviteId}
                  className="border-b border-zinc-200 align-top text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4">{invite.roleName}</td>
                  <td className="py-2 pr-4">{statusLabel(invite)}</td>
                  <td className="py-2">
                    {invite.status === "pending" && !invite.expired ? (
                      <button
                        type="button"
                        disabled={pendingInviteId === invite.inviteId}
                        onClick={() => handleRevoke(invite.inviteId)}
                        className="rounded border border-red-300 px-2 py-0.5 text-xs text-red-600 hover:bg-red-50 disabled:opacity-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950"
                      >
                        {t("invitesRevokeButton")}
                      </button>
                    ) : (
                      <span className="text-zinc-400 dark:text-zinc-600">—</span>
                    )}
                    {rowError?.inviteId === invite.inviteId && (
                      <p className="mt-1 text-xs text-red-600 dark:text-red-400">
                        {rowError.message}
                      </p>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
