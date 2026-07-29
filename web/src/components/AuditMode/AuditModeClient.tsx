"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).


import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import {
  fetchFirmPermissionAuditClient,
  grantPermissionClient,
  revokePermissionClient,
  type FirmPermissionAudit,
} from "./client";
import { useAuditBadges } from "./useAuditBadges";
import PermissionPopup from "./PermissionPopup";

type Props = {
  firmId: string | null;
  isOwner: boolean;
};

type PopupState = {
  permissionKey: string;
  anchorRect: DOMRect;
};

// Root client component for Vision §3's Permission Audit Mode, mounted
// once in the root layout (see app/[locale]/layout.tsx) so it works
// across every page, not just the ones built with it in mind. Two mostly
// independent halves:
//
//  1. A live-session listener (SSE via /api/permission-events/{firmId})
//     that's active for ANY authenticated firm member, not just owners -
//     Vision §3's "active sessions for the affected role are reconnected"
//     means the affected role's own sessions, not just an owner watching
//     Audit Mode.
//  2. The Audit Mode toggle/overlay itself, which only renders for
//     isOwner (see docs/DEVELOPMENT.md's "who can use it" note) - the
//     badge scan, the permission-audit fetch, and the grant/revoke popup.
export default function AuditModeClient({ firmId, isOwner }: Props) {
  const t = useTranslations("PermissionAudit");
  const router = useRouter();

  const [auditOn, setAuditOn] = useState(false);
  const [audit, setAudit] = useState<FirmPermissionAudit | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [popup, setPopup] = useState<PopupState | null>(null);

  const badges = useAuditBadges(auditOn);

  const reloadAudit = useCallback(async () => {
    if (!firmId) return;
    const result = await fetchFirmPermissionAuditClient(firmId);
    if (!result) {
      setLoadError(true);
      setAudit(null);
      return;
    }
    setLoadError(false);
    setAudit(result);
  }, [firmId]);

  // Fetch the matrix whenever Audit Mode is turned on. The off branch
  // deliberately does not reset `audit`/`popup` state here - the JSX
  // below only renders them while auditOn is true, so stale state simply
  // goes unused rather than needing a synchronous reset inside the
  // effect (React discourages calling setState directly in an effect
  // body; see useAuditBadges's identical reasoning).
  useEffect(() => {
    if (!auditOn || !firmId) return;
    let cancelled = false;
    fetchFirmPermissionAuditClient(firmId).then((result) => {
      if (cancelled) return;
      if (!result) {
        setLoadError(true);
        setAudit(null);
        return;
      }
      setLoadError(false);
      setAudit(result);
    });
    return () => {
      cancelled = true;
    };
  }, [auditOn, firmId]);

  // auditOn is read inside the SSE listener below via this ref, not as a
  // useEffect dependency - re-subscribing the EventSource itself every
  // time Audit Mode toggles would be wasteful and could briefly drop an
  // event arriving mid-reconnect; only firmId should recreate the
  // connection, while the listener should always see the latest auditOn.
  const auditOnRef = useRef(auditOn);
  useEffect(() => {
    auditOnRef.current = auditOn;
  }, [auditOn]);

  // Live-session propagation (Vision §3): any firm member subscribes,
  // regardless of isOwner or auditOn - a plain refresh of server-rendered
  // data is still valuable even when Audit Mode itself isn't open.
  useEffect(() => {
    if (!firmId) return;

    const source = new EventSource(`/api/permission-events/${firmId}`);
    source.addEventListener("permissions-changed", () => {
      router.refresh();
      if (auditOnRef.current) {
        fetchFirmPermissionAuditClient(firmId).then((result) => {
          if (result) setAudit(result);
        });
      }
    });

    return () => source.close();
  }, [firmId, router]);

  function openPopup(el: EventTarget | null, permissionKey: string) {
    if (!(el instanceof HTMLElement)) return;
    setPopup({ permissionKey, anchorRect: el.getBoundingClientRect() });
  }

  async function handleGrant(roleId: string) {
    if (!firmId || !popup) return;
    const ok = await grantPermissionClient(firmId, roleId, popup.permissionKey);
    if (!ok) throw new Error("grant failed");
    await reloadAudit();
  }

  async function handleRevoke(roleId: string) {
    if (!firmId || !popup) return;
    const ok = await revokePermissionClient(firmId, roleId, popup.permissionKey);
    if (!ok) throw new Error("revoke failed");
    await reloadAudit();
  }

  if (!firmId) return null;

  return (
    <>
      {isOwner && (
        <div
          data-audit-overlay-root
          className="fixed bottom-4 right-4 z-40"
        >
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setAuditOn((v) => !v)}
            className="rounded-full bg-foreground px-4 py-2 text-xs font-medium text-background shadow-lg hover:bg-[#383838] dark:hover:bg-[#ccc]"
          >
            {auditOn ? t("toggleOff") : t("toggleOn")}
          </button>
        </div>
      )}

      {auditOn &&
        typeof document !== "undefined" &&
        createPortal(
          <div data-audit-overlay-root>
            {loadError && (
              <p className="fixed bottom-16 right-4 z-40 max-w-xs rounded bg-red-600 px-3 py-2 text-xs text-white shadow-lg">
                {t("loadError")}
              </p>
            )}

            {audit &&
              badges.map((badge) => {
                const hasPermission =
                  badge.kind === "tagged" &&
                  !!badge.permissionKey &&
                  audit.myPermissionKeys.includes(badge.permissionKey);

                const label =
                  badge.kind === "untagged"
                    ? "!"
                    : hasPermission
                      ? "✓"
                      : "✗";

                const title =
                  badge.kind === "untagged"
                    ? t("badgeUntaggedAria")
                    : hasPermission
                      ? t("badgeOkAria")
                      : t("badgeMissingAria");

                const colorClasses =
                  badge.kind === "untagged"
                    ? "bg-amber-500 text-white"
                    : hasPermission
                      ? "bg-green-600 text-white"
                      : "bg-zinc-500 text-white";

                return (
                  <button
                    key={badge.id}
                    type="button"
                    data-permission-public="true"
                    title={title}
                    aria-label={title}
                    disabled={badge.kind === "untagged"}
                    onClick={(e) =>
                      badge.permissionKey &&
                      openPopup(e.currentTarget, badge.permissionKey)
                    }
                    className={`fixed z-40 flex h-4 w-4 items-center justify-center rounded-full text-[10px] leading-none shadow ${colorClasses} ${
                      badge.kind === "untagged" ? "" : "cursor-pointer"
                    }`}
                    style={{
                      top: badge.rect.top - 6,
                      left: badge.rect.right - 6,
                    }}
                  >
                    {label}
                  </button>
                );
              })}

            {popup && audit && (
              <PermissionPopup
                permissionKey={popup.permissionKey}
                anchorRect={popup.anchorRect}
                roles={audit.roles}
                onGrant={handleGrant}
                onRevoke={handleRevoke}
                onClose={() => setPopup(null)}
              />
            )}
          </div>,
          document.body,
        )}
    </>
  );
}
