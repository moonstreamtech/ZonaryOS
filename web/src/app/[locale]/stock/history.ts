// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { InstanceState } from "@/lib/workflow";
import type { AuditLogEntry } from "@/lib/auditlog";

export type HistoryRow = {
  id: string;
  occurredAt: string;
  actorEmail: string;
  actorDisplayName: string;
  action: string;
  itemName: string | null;
  toStateKey: string | null;
};

// Sale/transaction history (item 6) is built entirely from data the
// audit trail (internal/auditlog, Vision §3 / PR 8) and the workflow
// engine (internal/workflow, PR 7) already produce - no parallel history
// table or backend endpoint. Pulled out of StockHistory.tsx as a pure
// function so it's unit-testable without rendering (see history.test.ts),
// same pattern as components/AuditMode/useAuditBadges.ts's
// scanInteractiveElements.
//
// entries is assumed already most-recent-first (internal/auditlog.List's
// own ORDER BY) - this function preserves that order rather than
// re-sorting.
export function buildHistoryRows(
  instances: InstanceState[],
  entries: AuditLogEntry[],
): HistoryRow[] {
  const itemByInstance = new Map<string, string | null>(
    instances.map((i) => [
      i.instanceId,
      typeof i.payload.item === "string" ? i.payload.item : null,
    ]),
  );

  return entries
    .filter((e) => e.entityType === "workflow_instance")
    .map((e) => {
      const changesPayload =
        e.changes.payload && typeof e.changes.payload === "object"
          ? (e.changes.payload as Record<string, unknown>)
          : null;
      const fallbackItem =
        changesPayload && typeof changesPayload.item === "string"
          ? changesPayload.item
          : null;

      return {
        id: e.id,
        occurredAt: e.occurredAt,
        actorEmail: e.userEmail,
        actorDisplayName: e.userDisplayName,
        action: e.action,
        itemName: itemByInstance.get(e.entityId) ?? fallbackItem,
        toStateKey:
          typeof e.changes.to_state === "string" ? e.changes.to_state : null,
      };
    });
}
