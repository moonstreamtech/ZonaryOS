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
  toStateKey: string | null;
  payload: Record<string, unknown> | null;
};

// Generic replacement for the old stock-specific stock/history.ts:
// built entirely from data internal/auditlog and internal/workflow
// already produce, same as before - but no longer assumes a "payload.item"
// field. Each row's payload is whatever this specific write's delta
// carried (audit_log.changes.payload); when that delta is empty (e.g.
// record_sale's transitions, which the frontend always calls with an
// empty payload - see lib/workflow.ts's executeTransition default), it
// falls back to the instance's current full payload, same "best
// available context, not a hardcoded field name" approach
// components/Workflow/format.ts's formatPayload takes for rendering it.
//
// entries is assumed already most-recent-first (internal/auditlog.List's
// own ORDER BY) - this function preserves that order rather than
// re-sorting.
export function buildHistoryRows(
  instances: InstanceState[],
  entries: AuditLogEntry[],
): HistoryRow[] {
  const payloadByInstance = new Map<string, Record<string, unknown>>(
    instances.map((i) => [i.instanceId, i.payload]),
  );

  return entries
    .filter((e) => e.entityType === "workflow_instance")
    .map((e) => {
      const deltaPayload =
        e.changes.payload && typeof e.changes.payload === "object"
          ? (e.changes.payload as Record<string, unknown>)
          : null;
      const hasDelta = deltaPayload && Object.keys(deltaPayload).length > 0;

      return {
        id: e.id,
        occurredAt: e.occurredAt,
        actorEmail: e.userEmail,
        actorDisplayName: e.userDisplayName,
        action: e.action,
        toStateKey:
          typeof e.changes.to_state === "string" ? e.changes.to_state : null,
        payload: hasDelta
          ? deltaPayload
          : (payloadByInstance.get(e.entityId) ?? null),
      };
    });
}
