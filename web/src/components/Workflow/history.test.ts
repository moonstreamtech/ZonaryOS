// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import { buildHistoryRows } from "./history";
import type { InstanceState } from "@/lib/workflow";
import type { AuditLogEntry } from "@/lib/auditlog";

// Proves buildHistoryRows works against two structurally different
// workflow shapes - stock_to_sale (item/quantity) and a purchase_order-
// like definition (vendor/amount) - rather than only ever having been
// exercised against the one payload shape the original stock-specific
// version was written for.

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
  it("resolves payload from the current instance list for a stock_to_sale-shaped workflow", () => {
    const instances: InstanceState[] = [
      {
        instanceId: "instance-1",
        workflowDefinitionId: "def-1",
        state: { key: "sold", name: "Sold" },
        payload: { item: "Widget", quantity: 2 },
        availableActions: [],
      },
    ];

    const rows = buildHistoryRows(instances, [
      entry({ action: "record_sale", changes: { to_state: "sold", payload: {} } }),
    ]);

    expect(rows).toHaveLength(1);
    expect(rows[0].payload).toEqual({ item: "Widget", quantity: 2 });
    expect(rows[0].toStateKey).toBe("sold");
    expect(rows[0].action).toBe("record_sale");
  });

  it("resolves payload from the current instance list for a structurally different purchase_order-shaped workflow", () => {
    const instances: InstanceState[] = [
      {
        instanceId: "instance-2",
        workflowDefinitionId: "def-2",
        state: { key: "approved", name: "Approved" },
        payload: { vendor: "Acme Supply", amount: 199.99 },
        availableActions: [],
      },
    ];

    const rows = buildHistoryRows(instances, [
      entry({
        id: "entry-po",
        entityId: "instance-2",
        action: "approve",
        changes: { to_state: "approved", payload: {} },
      }),
    ]);

    expect(rows[0].payload).toEqual({ vendor: "Acme Supply", amount: 199.99 });
    expect(rows[0].action).toBe("approve");
  });

  it("prefers the audit delta's own payload when it's non-empty, over the instance's current payload", () => {
    const rows = buildHistoryRows([], [
      entry({
        entityId: "instance-3",
        action: "create",
        changes: { to_state: "draft", payload: { vendor: "New Vendor Co" } },
      }),
    ]);

    expect(rows[0].payload).toEqual({ vendor: "New Vendor Co" });
  });

  it("excludes entries for other entity types (e.g. the workflow_instance_list view log)", () => {
    const instances: InstanceState[] = [
      {
        instanceId: "instance-1",
        workflowDefinitionId: "def-1",
        state: { key: "in_stock", name: "In Stock" },
        payload: {},
        availableActions: [],
      },
    ];
    const rows = buildHistoryRows(instances, [
      entry({ entityType: "workflow_instance_list", action: "view" }),
    ]);

    expect(rows).toHaveLength(0);
  });

  it("preserves the backend's most-recent-first ordering rather than re-sorting", () => {
    const rows = buildHistoryRows([], [
      entry({ id: "newer", occurredAt: "2026-01-02T00:00:00Z" }),
      entry({ id: "older", occurredAt: "2026-01-01T00:00:00Z" }),
    ]);

    expect(rows.map((r) => r.id)).toEqual(["newer", "older"]);
  });

  it("leaves payload null when neither the instance nor the audit delta has one", () => {
    const rows = buildHistoryRows([], [entry({ entityId: "instance-unknown" })]);

    expect(rows[0].payload).toBeNull();
  });
});
