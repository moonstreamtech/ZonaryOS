// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import {
  validateBuilderSpec,
  type BuilderFieldRow,
  type BuilderSpec,
  type BuilderStateRow,
  type BuilderTransitionRow,
} from "./builderValidation";

// The workflow definition builder's client-side pre-check (item 1/2),
// mirroring internal/workflow's DefinitionSpec.Validate rule for rule -
// tested here independently of the form component itself, same pattern
// as this directory's history.test.ts/format.test.ts.

function state(overrides: Partial<BuilderStateRow>): BuilderStateRow {
  return { id: 1, key: "", name: "", isInitial: false, isTerminal: false, ...overrides };
}

function transition(overrides: Partial<BuilderTransitionRow>): BuilderTransitionRow {
  return {
    id: 1,
    fromStateKey: "",
    toStateKey: "",
    actionKey: "",
    name: "",
    permissionKey: "",
    permissionDescription: "",
    journalEnabled: false,
    journalDescription: "",
    journalLines: [],
    ...overrides,
  };
}

function field(overrides: Partial<BuilderFieldRow>): BuilderFieldRow {
  return {
    id: 1,
    name: "",
    type: "string",
    required: false,
    options: [],
    referenceDefinitionKey: "",
    arrayItemType: "string",
    ...overrides,
  };
}

function baseSpec(overrides: Partial<BuilderSpec>): BuilderSpec {
  return {
    key: "purchase_order",
    name: "Purchase Order",
    createPermissionKey: "workflow.purchase_order.create",
    createPermissionDescription: "",
    states: [],
    transitions: [],
    fields: [],
    ...overrides,
  };
}

describe("validateBuilderSpec", () => {
  it("accepts a well-formed spec - a purchase_order-shaped two-transition workflow", () => {
    const spec = baseSpec({
      states: [
        state({ id: 1, key: "draft", name: "Draft", isInitial: true }),
        state({ id: 2, key: "approved", name: "Approved" }),
        state({ id: 3, key: "received", name: "Received", isTerminal: true }),
      ],
      transitions: [
        transition({
          id: 1,
          fromStateKey: "draft",
          toStateKey: "approved",
          actionKey: "approve",
          name: "Approve",
          permissionKey: "workflow.purchase_order.approve",
        }),
        transition({
          id: 2,
          fromStateKey: "approved",
          toStateKey: "received",
          actionKey: "receive",
          name: "Receive",
          permissionKey: "workflow.purchase_order.receive",
        }),
      ],
    });

    expect(validateBuilderSpec(spec)).toEqual([]);
  });

  it("accepts a structurally different single-state, no-transition spec (a minimal, different shape)", () => {
    const spec = baseSpec({
      key: "simple_log",
      name: "Simple Log",
      states: [state({ id: 1, key: "logged", name: "Logged", isInitial: true, isTerminal: true })],
    });

    expect(validateBuilderSpec(spec)).toEqual([]);
  });

  it("flags a missing key/name/create permission key", () => {
    const errors = validateBuilderSpec(
      baseSpec({ key: "", name: "", createPermissionKey: "", states: [state({ key: "s", isInitial: true })] }),
    );

    expect(errors).toContainEqual({ code: "keyRequired" });
    expect(errors).toContainEqual({ code: "nameRequired" });
    expect(errors).toContainEqual({ code: "createPermissionKeyRequired" });
  });

  it("flags zero states", () => {
    const errors = validateBuilderSpec(baseSpec({ states: [] }));
    expect(errors).toContainEqual({ code: "noStates" });
  });

  it("flags a duplicate state key", () => {
    const errors = validateBuilderSpec(
      baseSpec({
        states: [
          state({ id: 1, key: "draft", isInitial: true }),
          state({ id: 2, key: "draft" }),
        ],
      }),
    );
    expect(errors).toContainEqual({ code: "duplicateStateKey", key: "draft" });
  });

  it("flags zero initial states", () => {
    const errors = validateBuilderSpec(
      baseSpec({ states: [state({ id: 1, key: "draft" })] }),
    );
    expect(errors).toContainEqual({ code: "notExactlyOneInitialState", count: 0 });
  });

  it("flags more than one initial state", () => {
    const errors = validateBuilderSpec(
      baseSpec({
        states: [
          state({ id: 1, key: "draft", isInitial: true }),
          state({ id: 2, key: "approved", isInitial: true }),
        ],
      }),
    );
    expect(errors).toContainEqual({ code: "notExactlyOneInitialState", count: 2 });
  });

  it("flags a transition referencing an unknown from-state or to-state - the real states list, not free text", () => {
    const errors = validateBuilderSpec(
      baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        transitions: [
          transition({
            fromStateKey: "draft",
            toStateKey: "nonexistent",
            actionKey: "approve",
            permissionKey: "workflow.purchase_order.approve",
          }),
        ],
      }),
    );
    expect(errors).toContainEqual({ code: "unknownToState", key: "nonexistent" });
  });

  it("flags a transition from an unknown state", () => {
    const errors = validateBuilderSpec(
      baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        transitions: [
          transition({
            fromStateKey: "nonexistent",
            toStateKey: "draft",
            actionKey: "approve",
            permissionKey: "workflow.purchase_order.approve",
          }),
        ],
      }),
    );
    expect(errors).toContainEqual({ code: "unknownFromState", key: "nonexistent" });
  });

  it("flags a transition missing an action key or permission key", () => {
    const errors = validateBuilderSpec(
      baseSpec({
        states: [
          state({ id: 1, key: "draft", isInitial: true }),
          state({ id: 2, key: "approved" }),
        ],
        transitions: [
          transition({ fromStateKey: "draft", toStateKey: "approved", actionKey: "", permissionKey: "" }),
        ],
      }),
    );
    expect(errors).toContainEqual({ code: "transitionActionKeyRequired" });
    expect(errors).toContainEqual({ code: "transitionPermissionKeyRequired" });
  });

  it("flags two transitions sharing the same (from-state, action key) pair", () => {
    const errors = validateBuilderSpec(
      baseSpec({
        states: [
          state({ id: 1, key: "draft", isInitial: true }),
          state({ id: 2, key: "approved" }),
          state({ id: 3, key: "rejected" }),
        ],
        transitions: [
          transition({
            id: 1,
            fromStateKey: "draft",
            toStateKey: "approved",
            actionKey: "decide",
            permissionKey: "workflow.purchase_order.decide",
          }),
          transition({
            id: 2,
            fromStateKey: "draft",
            toStateKey: "rejected",
            actionKey: "decide",
            permissionKey: "workflow.purchase_order.decide",
          }),
        ],
      }),
    );
    expect(errors).toContainEqual({
      code: "duplicateTransition",
      fromStateKey: "draft",
      actionKey: "decide",
    });
  });

  // Open Points item 35's builder-side validation: an empty fields array
  // (the default - see DefinitionBuilder.tsx's own useState) never
  // produces a validation error, exactly the "optional, no new required
  // section" contract the batch requires.
  it("accepts an empty fields array (no payload schema defined)", () => {
    const spec = baseSpec({
      states: [state({ id: 1, key: "draft", isInitial: true })],
      fields: [],
    });
    expect(validateBuilderSpec(spec)).toEqual([]);
  });

  it("accepts a well-formed multi-type field schema", () => {
    const spec = baseSpec({
      states: [state({ id: 1, key: "draft", isInitial: true })],
      fields: [
        field({ id: 1, name: "item", type: "string", required: true }),
        field({ id: 2, name: "quantity", type: "number", required: true }),
        field({ id: 3, name: "rush", type: "boolean", required: false }),
        field({ id: 4, name: "dueDate", type: "date", required: false }),
      ],
    });
    expect(validateBuilderSpec(spec)).toEqual([]);
  });

  it("flags a payload field with an empty name", () => {
    const errors = validateBuilderSpec(
      baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [field({ id: 1, name: "" })],
      }),
    );
    expect(errors).toContainEqual({ code: "fieldNameRequired" });
  });

  it("flags two payload fields sharing the same name", () => {
    const errors = validateBuilderSpec(
      baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [
          field({ id: 1, name: "item", type: "string" }),
          field({ id: 2, name: "item", type: "number" }),
        ],
      }),
    );
    expect(errors).toContainEqual({ code: "duplicateFieldName", name: "item" });
  });

  // Open Points item 38's builder-side checks.
  describe("enum fields", () => {
    it("accepts a well-formed enum field", () => {
      const spec = baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [field({ id: 1, name: "priority", type: "enum", options: ["low", "high"] })],
      });
      expect(validateBuilderSpec(spec)).toEqual([]);
    });

    it("flags an enum field with zero options", () => {
      const spec = baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [field({ id: 1, name: "priority", type: "enum", options: [] })],
      });
      expect(validateBuilderSpec(spec)).toContainEqual({ code: "enumOptionsRequired", name: "priority" });
    });

    it("flags an empty-string enum option", () => {
      const spec = baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [field({ id: 1, name: "priority", type: "enum", options: ["low", ""] })],
      });
      expect(validateBuilderSpec(spec)).toContainEqual({ code: "enumOptionEmpty", name: "priority" });
    });

    it("flags a duplicate enum option", () => {
      const spec = baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [field({ id: 1, name: "priority", type: "enum", options: ["low", "low"] })],
      });
      expect(validateBuilderSpec(spec)).toContainEqual({
        code: "enumOptionDuplicate",
        name: "priority",
        option: "low",
      });
    });
  });

  describe("reference fields", () => {
    it("accepts a well-formed reference field", () => {
      const spec = baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [field({ id: 1, name: "relatedStock", type: "reference", referenceDefinitionKey: "stock_to_sale" })],
      });
      expect(validateBuilderSpec(spec)).toEqual([]);
    });

    it("flags a reference field with no target definition key chosen", () => {
      const spec = baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [field({ id: 1, name: "relatedStock", type: "reference", referenceDefinitionKey: "" })],
      });
      expect(validateBuilderSpec(spec)).toContainEqual({
        code: "referenceDefinitionKeyRequired",
        name: "relatedStock",
      });
    });
  });

  describe("array fields", () => {
    it("accepts a well-formed array field", () => {
      const spec = baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [field({ id: 1, name: "tags", type: "array", arrayItemType: "string" })],
      });
      expect(validateBuilderSpec(spec)).toEqual([]);
    });

    it("flags an array field with no item type chosen", () => {
      const spec = baseSpec({
        states: [state({ id: 1, key: "draft", isInitial: true })],
        fields: [field({ id: 1, name: "tags", type: "array", arrayItemType: "" as never })],
      });
      expect(validateBuilderSpec(spec)).toContainEqual({ code: "arrayItemTypeRequired", name: "tags" });
    });
  });

  describe("journal templates", () => {
    it("accepts a well-formed journal template", () => {
      const spec = baseSpec({
        states: [
          state({ id: 1, key: "in_stock", isInitial: true }),
          state({ id: 2, key: "sold" }),
        ],
        transitions: [
          transition({
            fromStateKey: "in_stock",
            toStateKey: "sold",
            actionKey: "record_sale",
            permissionKey: "workflow.stock_to_sale.record_sale",
            journalEnabled: true,
            journalDescription: "Sale of {{item}}",
            journalLines: [
              { id: 1, accountCode: "1100", side: "debit", amountField: "quantity*unit_price" },
              { id: 2, accountCode: "4000", side: "credit", amountField: "quantity*unit_price" },
            ],
          }),
        ],
      });
      expect(validateBuilderSpec(spec)).toEqual([]);
    });

    it("ignores a disabled journal template entirely", () => {
      const spec = baseSpec({
        states: [
          state({ id: 1, key: "in_stock", isInitial: true }),
          state({ id: 2, key: "sold" }),
        ],
        transitions: [
          transition({
            fromStateKey: "in_stock",
            toStateKey: "sold",
            actionKey: "record_sale",
            permissionKey: "workflow.stock_to_sale.record_sale",
            journalEnabled: false,
            journalLines: [],
          }),
        ],
      });
      expect(validateBuilderSpec(spec)).toEqual([]);
    });

    it("flags an enabled journal template with an empty description", () => {
      const spec = baseSpec({
        states: [
          state({ id: 1, key: "in_stock", isInitial: true }),
          state({ id: 2, key: "sold" }),
        ],
        transitions: [
          transition({
            fromStateKey: "in_stock",
            toStateKey: "sold",
            actionKey: "record_sale",
            permissionKey: "workflow.stock_to_sale.record_sale",
            journalEnabled: true,
            journalDescription: "",
            journalLines: [
              { id: 1, accountCode: "1100", side: "debit", amountField: "quantity" },
              { id: 2, accountCode: "4000", side: "credit", amountField: "quantity" },
            ],
          }),
        ],
      });
      expect(validateBuilderSpec(spec)).toContainEqual({
        code: "journalDescriptionRequired",
        actionKey: "record_sale",
      });
    });

    it("flags an enabled journal template with fewer than two lines", () => {
      const spec = baseSpec({
        states: [
          state({ id: 1, key: "in_stock", isInitial: true }),
          state({ id: 2, key: "sold" }),
        ],
        transitions: [
          transition({
            fromStateKey: "in_stock",
            toStateKey: "sold",
            actionKey: "record_sale",
            permissionKey: "workflow.stock_to_sale.record_sale",
            journalEnabled: true,
            journalDescription: "Sale",
            journalLines: [{ id: 1, accountCode: "1100", side: "debit", amountField: "quantity" }],
          }),
        ],
      });
      expect(validateBuilderSpec(spec)).toContainEqual({
        code: "journalNeedsTwoLines",
        actionKey: "record_sale",
      });
    });

    it("flags a journal line missing an account code or amount field", () => {
      const spec = baseSpec({
        states: [
          state({ id: 1, key: "in_stock", isInitial: true }),
          state({ id: 2, key: "sold" }),
        ],
        transitions: [
          transition({
            fromStateKey: "in_stock",
            toStateKey: "sold",
            actionKey: "record_sale",
            permissionKey: "workflow.stock_to_sale.record_sale",
            journalEnabled: true,
            journalDescription: "Sale",
            journalLines: [
              { id: 1, accountCode: "", side: "debit", amountField: "quantity" },
              { id: 2, accountCode: "4000", side: "credit", amountField: "" },
            ],
          }),
        ],
      });
      const errors = validateBuilderSpec(spec);
      expect(errors).toContainEqual({ code: "journalLineAccountCodeRequired", actionKey: "record_sale" });
      expect(errors).toContainEqual({ code: "journalLineAmountFieldRequired", actionKey: "record_sale" });
    });
  });
});
