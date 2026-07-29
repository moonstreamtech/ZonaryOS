"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).


import { useState } from "react";
import { useTranslations } from "next-intl";
import type { RoleGrant } from "@/lib/permissionAudit";

type Props = {
  permissionKey: string;
  anchorRect: DOMRect;
  roles: RoleGrant[];
  onGrant: (roleId: string) => Promise<void>;
  onRevoke: (roleId: string) => Promise<void>;
  onClose: () => void;
};

// Vision §3: "Clicking the badge opens a popup that lists: the roles
// that have this permission (each with a 'remove' option), and a
// dropdown to select and 'add' permission to a role that doesn't have it
// yet, instantly." Owner-flagged roles are never in `roles` at all (the
// backend already excluded them - see GetFirmPermissionAudit), so there
// is nothing to filter out here.
export default function PermissionPopup({
  permissionKey,
  anchorRect,
  roles,
  onGrant,
  onRevoke,
  onClose,
}: Props) {
  const t = useTranslations("PermissionAudit");
  const [pendingRoleId, setPendingRoleId] = useState<string | null>(null);
  const [selectedRoleId, setSelectedRoleId] = useState("");
  const [error, setError] = useState<string | null>(null);

  const rolesWithPermission = roles.filter((r) =>
    r.permissionKeys.includes(permissionKey),
  );
  const rolesWithoutPermission = roles.filter(
    (r) => !r.permissionKeys.includes(permissionKey),
  );

  async function handleRemove(roleId: string) {
    setError(null);
    setPendingRoleId(roleId);
    try {
      await onRevoke(roleId);
    } catch {
      setError(t("actionError"));
    } finally {
      setPendingRoleId(null);
    }
  }

  async function handleAdd() {
    if (!selectedRoleId) return;
    setError(null);
    setPendingRoleId(selectedRoleId);
    try {
      await onGrant(selectedRoleId);
      setSelectedRoleId("");
    } catch {
      setError(t("actionError"));
    } finally {
      setPendingRoleId(null);
    }
  }

  return (
    <div
      data-audit-overlay-root
      className="fixed z-50 w-72 rounded-lg border border-zinc-300 bg-white p-3 text-sm shadow-lg dark:border-zinc-700 dark:bg-zinc-900"
      style={{
        top: anchorRect.bottom + 6,
        left: Math.max(8, anchorRect.right - 288),
      }}
    >
      <div className="mb-2 flex items-start justify-between gap-2">
        <p className="font-medium text-black dark:text-zinc-50">
          {t("popupTitle")}
        </p>
        <button
          type="button"
          data-permission-public="true"
          onClick={onClose}
          aria-label={t("closeButton")}
          className="text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200"
        >
          ×
        </button>
      </div>
      <p className="mb-2 break-all font-mono text-xs text-zinc-500 dark:text-zinc-400">
        {permissionKey}
      </p>

      {error && (
        <p className="mb-2 text-xs text-red-600 dark:text-red-400">{error}</p>
      )}

      <p className="mb-1 text-xs font-medium text-zinc-600 dark:text-zinc-400">
        {t("rolesWithPermission")}
      </p>
      {rolesWithPermission.length === 0 ? (
        <p className="mb-2 text-xs text-zinc-400 dark:text-zinc-600">
          {t("noRoles")}
        </p>
      ) : (
        <ul className="mb-2 flex flex-col gap-1">
          {rolesWithPermission.map((role) => (
            <li
              key={role.roleId}
              className="flex items-center justify-between gap-2"
            >
              <span className="text-black dark:text-zinc-50">
                {role.roleName}
              </span>
              <button
                type="button"
                data-permission-public="true"
                disabled={pendingRoleId === role.roleId}
                onClick={() => handleRemove(role.roleId)}
                className="rounded border border-red-300 px-2 py-0.5 text-xs text-red-600 hover:bg-red-50 disabled:opacity-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950"
              >
                {t("removeButton")}
              </button>
            </li>
          ))}
        </ul>
      )}

      {rolesWithoutPermission.length > 0 && (
        <div className="flex items-center gap-2 border-t border-zinc-200 pt-2 dark:border-zinc-800">
          <select
            data-permission-public="true"
            value={selectedRoleId}
            onChange={(e) => setSelectedRoleId(e.target.value)}
            className="flex-1 rounded border border-zinc-300 bg-white px-2 py-1 text-xs text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
          >
            <option value="">{t("selectRolePlaceholder")}</option>
            {rolesWithoutPermission.map((role) => (
              <option key={role.roleId} value={role.roleId}>
                {role.roleName}
              </option>
            ))}
          </select>
          <button
            type="button"
            data-permission-public="true"
            disabled={!selectedRoleId || pendingRoleId === selectedRoleId}
            onClick={handleAdd}
            className="rounded bg-foreground px-2 py-1 text-xs font-medium text-background disabled:opacity-50"
          >
            {t("addButton")}
          </button>
        </div>
      )}
    </div>
  );
}
