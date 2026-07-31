"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { RoleGrant } from "@/lib/permissionAudit";

type Props = {
  firmId: string;
  // Non-owner roles only - GetFirmPermissionAudit already excludes the
  // is_owner role (Vision §3: "roles at the highest permission tier ...
  // don't appear in these lists"), so there is nothing to filter out
  // here; the owner role simply never reaches this component, which is
  // what keeps it structurally un-renameable/undeletable from the UI
  // side (the backend rejects it either way - see RenameRole/DeleteRole's
  // ErrOwnerRoleImmutable - this is belt-and-suspenders, not the actual
  // enforcement boundary).
  roles: RoleGrant[];
};

// Item 5's frontend half: role create/rename/delete. Deliberately does
// NOT render each role's own permission grants - the permission catalog
// table this component is always rendered alongside (PermissionSettings's
// page.tsx) already shows, for every permission key, which role keys
// hold it; a newly created role simply doesn't appear in any row's role
// chips yet, which is what "starts with zero grants" looks like on that
// existing table. Building a second "this role's grants" view here would
// duplicate that render path instead of reusing it.
export default function RoleManager({ firmId, roles }: Props) {
  const t = useTranslations("PermissionSettings");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [newKey, setNewKey] = useState("");
  const [newName, setNewName] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [renamingRoleId, setRenamingRoleId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [rowError, setRowError] = useState<{ roleId: string; message: string } | null>(null);
  const [pendingRoleId, setPendingRoleId] = useState<string | null>(null);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    const key = newKey.trim();
    const name = newName.trim();
    if (!key) {
      setCreateError(t("createRoleKeyRequired"));
      return;
    }
    if (!name) {
      setCreateError(t("createRoleNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/permission-audit/${firmId}/roles`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key, name }),
      });
      if (!res.ok) {
        setCreateError(
          res.status === 409 ? t("createRoleKeyExists") : t("createRoleError"),
        );
        return;
      }
      setCreating(false);
      setNewKey("");
      setNewName("");
      router.refresh();
    } catch {
      setCreateError(t("createRoleError"));
    } finally {
      setSubmitting(false);
    }
  }

  function startRename(role: RoleGrant) {
    setRowError(null);
    setRenamingRoleId(role.roleId);
    setRenameValue(role.roleName);
  }

  async function submitRename(roleId: string) {
    const name = renameValue.trim();
    if (!name) {
      setRowError({ roleId, message: t("createRoleNameRequired") });
      return;
    }
    setRowError(null);
    setPendingRoleId(roleId);
    try {
      const res = await fetch(`/api/permission-audit/${firmId}/roles/${roleId}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      if (!res.ok) {
        setRowError({ roleId, message: t("renameRoleError") });
        return;
      }
      setRenamingRoleId(null);
      router.refresh();
    } catch {
      setRowError({ roleId, message: t("renameRoleError") });
    } finally {
      setPendingRoleId(null);
    }
  }

  async function handleDelete(roleId: string) {
    if (!window.confirm(t("deleteRoleConfirm"))) return;
    setRowError(null);
    setPendingRoleId(roleId);
    try {
      const res = await fetch(`/api/permission-audit/${firmId}/roles/${roleId}`, {
        method: "DELETE",
      });
      if (!res.ok) {
        setRowError({
          roleId,
          message: res.status === 409 ? t("deleteRoleHasMembers") : t("deleteRoleError"),
        });
        return;
      }
      router.refresh();
    } catch {
      setRowError({ roleId, message: t("deleteRoleError") });
    } finally {
      setPendingRoleId(null);
    }
  }

  return (
    <div className="w-full max-w-4xl">
      <div className="mb-2 flex items-center justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold text-black dark:text-zinc-50">
            {t("manageRolesTitle")}
          </h2>
          <p className="text-sm text-zinc-600 dark:text-zinc-400">
            {t("manageRolesDescription")}
          </p>
        </div>
        {!creating && (
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating(true)}
            className="shrink-0 rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
          >
            {t("createRoleButton")}
          </button>
        )}
      </div>

      {creating && (
        <form
          onSubmit={submitCreate}
          className="mb-4 flex flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950 sm:flex-row sm:items-end"
        >
          <div className="flex-1">
            <label className="mb-1 block text-xs font-medium text-zinc-600 dark:text-zinc-400">
              {t("createRoleKeyLabel")}
            </label>
            <input
              type="text"
              value={newKey}
              onChange={(e) => setNewKey(e.target.value)}
              placeholder={t("createRoleKeyPlaceholder")}
              className="w-full rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
            />
          </div>
          <div className="flex-1">
            <label className="mb-1 block text-xs font-medium text-zinc-600 dark:text-zinc-400">
              {t("createRoleNameLabel")}
            </label>
            <input
              type="text"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder={t("createRoleNamePlaceholder")}
              className="w-full rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
            />
          </div>
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={submitting}
              className="rounded-md bg-foreground px-4 py-1.5 text-xs font-medium text-background disabled:opacity-50"
            >
              {t("createRoleSubmit")}
            </button>
            <button
              type="button"
              onClick={() => {
                setCreating(false);
                setCreateError(null);
              }}
              className="rounded-md border border-zinc-300 px-4 py-1.5 text-xs font-medium text-black dark:border-zinc-700 dark:text-zinc-50"
            >
              {t("createRoleCancel")}
            </button>
          </div>
          {createError && (
            <p className="w-full text-xs text-red-600 dark:text-red-400">{createError}</p>
          )}
        </form>
      )}

      {roles.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("rolesEmpty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("roleColumnKey")}</th>
                <th className="py-2 pr-4 font-medium">{t("roleColumnName")}</th>
                <th className="py-2 font-medium">{t("roleColumnActions")}</th>
              </tr>
            </thead>
            <tbody>
              {roles.map((role) => (
                <tr
                  key={role.roleId}
                  className="border-b border-zinc-200 align-top text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4 font-mono text-xs whitespace-nowrap">
                    {role.roleKey}
                  </td>
                  <td className="py-2 pr-4">
                    {renamingRoleId === role.roleId ? (
                      <input
                        type="text"
                        value={renameValue}
                        onChange={(e) => setRenameValue(e.target.value)}
                        className="w-full rounded-md border border-zinc-300 bg-white px-2 py-1 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
                      />
                    ) : (
                      role.roleName
                    )}
                  </td>
                  <td className="py-2">
                    <div className="flex flex-wrap items-center gap-2">
                      {renamingRoleId === role.roleId ? (
                        <>
                          <button
                            type="button"
                            disabled={pendingRoleId === role.roleId}
                            onClick={() => submitRename(role.roleId)}
                            className="rounded border border-zinc-300 px-2 py-0.5 text-xs disabled:opacity-50 dark:border-zinc-700"
                          >
                            {t("renameRoleSubmit")}
                          </button>
                          <button
                            type="button"
                            onClick={() => setRenamingRoleId(null)}
                            className="rounded border border-zinc-300 px-2 py-0.5 text-xs dark:border-zinc-700"
                          >
                            {t("renameRoleCancel")}
                          </button>
                        </>
                      ) : (
                        <>
                          <button
                            type="button"
                            disabled={pendingRoleId === role.roleId}
                            onClick={() => startRename(role)}
                            className="rounded border border-zinc-300 px-2 py-0.5 text-xs disabled:opacity-50 dark:border-zinc-700"
                          >
                            {t("renameRoleButton")}
                          </button>
                          <button
                            type="button"
                            disabled={pendingRoleId === role.roleId}
                            onClick={() => handleDelete(role.roleId)}
                            className="rounded border border-red-300 px-2 py-0.5 text-xs text-red-600 hover:bg-red-50 disabled:opacity-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950"
                          >
                            {t("deleteRoleButton")}
                          </button>
                        </>
                      )}
                    </div>
                    {rowError?.roleId === role.roleId && (
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
