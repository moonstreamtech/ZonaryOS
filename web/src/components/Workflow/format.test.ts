// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import { formatPayload } from "./format";

// Proves formatPayload is genuinely shape-agnostic (item 2's payload
// editor and item 1's instance list both depend on this): a
// stock_to_sale-shaped payload and a structurally unrelated
// purchase_order-shaped payload (different field names, different value
// types) both render correctly with zero special-casing - see
// internal/workflow/workflow_integration_test.go's purchaseOrderSpec for
// the backend-side half of this same proof.

describe("formatPayload", () => {
  it("renders a stock_to_sale-shaped payload", () => {
    expect(formatPayload({ item: "Widget", quantity: 5 })).toBe(
      "item: Widget, quantity: 5",
    );
  });

  it("renders a structurally different purchase_order-shaped payload with no special-casing", () => {
    expect(
      formatPayload({ vendor: "Acme Supply", amount: 199.99, rushOrder: true }),
    ).toBe("vendor: Acme Supply, amount: 199.99, rushOrder: true");
  });

  it("renders an empty string for an empty payload", () => {
    expect(formatPayload({})).toBe("");
  });

  it("renders an empty string for a null/undefined payload", () => {
    expect(formatPayload(null)).toBe("");
    expect(formatPayload(undefined)).toBe("");
  });

  it("renders null/undefined field values as an em dash", () => {
    expect(formatPayload({ note: null })).toBe("note: —");
  });

  it("JSON-stringifies a nested object value rather than [object Object]", () => {
    expect(formatPayload({ address: { city: "Ankara" } })).toBe(
      'address: {"city":"Ankara"}',
    );
  });
});
