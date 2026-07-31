// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import {
  validateBuilderSpec,
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
});
