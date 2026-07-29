"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).


import { useEffect, useState } from "react";

export type BadgeTarget = {
  id: number;
  rect: DOMRect;
  kind: "tagged" | "untagged";
  permissionKey?: string;
};

// Elements this hook never scans - the Audit Mode overlay's own controls
// (toggle button, badges, popup) live inside a container carrying this
// attribute, so the mechanism never flags itself as "untagged" (see
// docs/DEVELOPMENT.md's note on distinguishing untagged from
// intentionally public).
const OVERLAY_ROOT_SELECTOR = "[data-audit-overlay-root]";

// Selector for "interactive element" per Vision §3's "every permission-
// gated button/link" - buttons and links that actually navigate/act.
// Deliberately not [role=button] or other ARIA-only cases; this repo
// only uses real <button>/<a> elements today.
const INTERACTIVE_SELECTOR = "button, a[href]";

/**
 * The pure DOM-scanning half of the badge overlay, exported separately
 * from the useAuditBadges hook so it's unit-testable with plain jsdom -
 * no React rendering needed to verify the classification rules
 * themselves (tagged / untagged / intentionally public / excluded as
 * overlay-internal). See useAuditBadges.test.ts.
 */
export function scanInteractiveElements(root: ParentNode = document): BadgeTarget[] {
  const elements = Array.from(
    root.querySelectorAll<HTMLElement>(INTERACTIVE_SELECTOR),
  ).filter((el) => !el.closest(OVERLAY_ROOT_SELECTOR));

  const targets: BadgeTarget[] = [];
  elements.forEach((el, index) => {
    if (el.dataset.permissionPublic === "true") {
      return; // intentionally public - no badge at all
    }
    const permissionKey = el.dataset.permissionKey;
    targets.push(
      permissionKey
        ? { id: index, rect: el.getBoundingClientRect(), kind: "tagged", permissionKey }
        : { id: index, rect: el.getBoundingClientRect(), kind: "untagged" },
    );
  });
  return targets;
}

/**
 * Scans the live DOM for every tagged/untagged interactive element and
 * keeps their positions current - Vision §3's badge overlay needs real
 * viewport coordinates (fixed-position badges, not children of whatever
 * component rendered the original element) so it can annotate elements
 * rendered anywhere on the page without reaching into their React tree.
 * Rescans on scroll/resize (cheap, just re-reads rects) and on DOM
 * mutations (debounced - e.g. router.refresh() re-rendering the stock
 * table after a sale). Only active while `enabled` is true.
 */
export function useAuditBadges(enabled: boolean): BadgeTarget[] {
  const [targets, setTargets] = useState<BadgeTarget[]>([]);

  useEffect(() => {
    // No setState call in this branch on purpose (React discourages
    // synchronous setState directly in an effect body): the hook masks
    // stale `targets` at the return statement below instead, so nothing
    // needs "resetting" when audit mode turns off.
    if (!enabled) {
      return;
    }

    let debounceHandle: ReturnType<typeof setTimeout> | null = null;
    const rescan = () => setTargets(scanInteractiveElements());
    const debouncedRescan = () => {
      if (debounceHandle) clearTimeout(debounceHandle);
      debounceHandle = setTimeout(rescan, 150);
    };

    rescan();

    window.addEventListener("scroll", rescan, true);
    window.addEventListener("resize", rescan);

    const observer = new MutationObserver(debouncedRescan);
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["data-permission-key", "data-permission-public"],
    });

    return () => {
      window.removeEventListener("scroll", rescan, true);
      window.removeEventListener("resize", rescan);
      observer.disconnect();
      if (debounceHandle) clearTimeout(debounceHandle);
    };
  }, [enabled]);

  return enabled ? targets : [];
}
