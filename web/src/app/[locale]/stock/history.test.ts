// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import { buildHistoryRows } from "./history";
import type { InstanceState } from "@/lib/workflow";
import type { AuditLogEntry } from "@/lib/auditlog";

// Item 6: the sale/transaction history's row-building logic, tested
// independently of rendering - same "extract the pure logic, test that"
// pattern components/AuditMode/useAuditBadges.test.ts established.

const instances: InstanceState[] = [
  {
    instanceId: "instance-1",
    workflowDefinitionId: "def-1",
    state: { key: "sold", name: "Sold" },
    payload: { item: "Widget", quantity: 2 },
    availableActions: [],
  },
];

function entry(overrides: Partial<AuditLogEntry>): AuditLogEntry {
  return {
    id: "entry-1",
    entityType: "workflow_instance",
    entityId: "instance-1",
    action: "create",
    changes: {},
    userId: "user-1",
    userEmail: "alice@example.com",
    userDisplayName: "Alice",
    occurredAt: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("buildHistoryRows", () => {
  it("resolves the item name from the current instance list, not the audit delta", () => {
    const rows = buildHistoryRows(instances, [
      entry({ action: "record_sale", changes: { to_state: "sold", payload: {} } }),
    ]);

    expect(rows).toHaveLength(1);
    expect(rows[0].itemName).toBe("Widget");
    expect(rows[0].toStateKey).toBe("sold");
    expect(rows[0].action).toBe("record_sale");
  });

  it("falls back to the audit entry's own payload delta when the instance is no longer in the list", () => {
    const rows = buildHistoryRows([], [
      entry({
        entityId: "instance-2",
        action: "create",
        changes: { to_state: "in_stock", payload: { item: "Gadget" } },
      }),
    ]);

    expect(rows[0].itemName).toBe("Gadget");
  });

  it("excludes entries for other entity types (e.g. the workflow_instance_list view log)", () => {
    const rows = buildHistoryRows(instances, [
      entry({ entityType: "workflow_instance_list", action: "view" }),
    ]);

    expect(rows).toHaveLength(0);
  });

  it("preserves the backend's most-recent-first ordering rather than re-sorting", () => {
    const rows = buildHistoryRows(instances, [
      entry({ id: "newer", occurredAt: "2026-01-02T00:00:00Z" }),
      entry({ id: "older", occurredAt: "2026-01-01T00:00:00Z" }),
    ]);

    expect(rows.map((r) => r.id)).toEqual(["newer", "older"]);
  });

  it("leaves itemName null when neither the instance nor the audit delta has one", () => {
    const rows = buildHistoryRows([], [entry({ entityId: "instance-3" })]);

    expect(rows[0].itemName).toBeNull();
  });
});
