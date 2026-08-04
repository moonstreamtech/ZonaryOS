// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import type { FieldSpecInput } from "@/lib/workflow";
import {
  actionRowToRuleAction,
  addChildToLogicNode,
  coerceValue,
  emptyActionRow,
  emptyLeafNode,
  emptyLogicNode,
  nodeToExpressionNode,
  removeNodeById,
  updateNodeById,
  wrapNodeInGroup,
  type BuilderNode,
} from "./ruleBuilderState";

// RuleBuilder's serialization logic (BuilderNode -> ExpressionNode,
// BuilderActionRow -> RuleAction) and its tree-mutation helpers, tested
// independently of the component's own rendering - same pattern as
// builderValidation.test.ts for the workflow definition builder.

const numberField: FieldSpecInput = { name: "quantity", type: "number", required: false };
const boolField: FieldSpecInput = { name: "urgent", type: "boolean", required: false };
const stringField: FieldSpecInput = { name: "item", type: "string", required: false };
const fields: FieldSpecInput[] = [numberField, boolField, stringField];

describe("coerceValue", () => {
  it("parses a numeric string when the field is a number", () => {
    expect(coerceValue("10", "number")).toBe(10);
  });

  it("falls back to the raw string when a 'number' field's value doesn't parse", () => {
    expect(coerceValue("not-a-number", "number")).toBe("not-a-number");
  });

  it("maps 'true'/'false' to real booleans for a boolean field", () => {
    expect(coerceValue("true", "boolean")).toBe(true);
    expect(coerceValue("false", "boolean")).toBe(false);
    expect(coerceValue("anything-else", "boolean")).toBe(false);
  });

  it("leaves a string field (or an unknown field type) as a plain string", () => {
    expect(coerceValue("widget", "string")).toBe("widget");
    expect(coerceValue("widget", undefined)).toBe("widget");
  });
});

describe("nodeToExpressionNode", () => {
  it("serializes a 'field' leaf, coercing its value by the field's schema type", () => {
    const node: BuilderNode = { ...emptyLeafNode(), leafType: "field", field: "quantity", op: "gt", value: "5" };
    expect(nodeToExpressionNode(node, fields)).toEqual({ type: "field", field: "quantity", op: "gt", value: 5 });
  });

  it("serializes a 'field' leaf against a boolean field", () => {
    const node: BuilderNode = { ...emptyLeafNode(), leafType: "field", field: "urgent", op: "eq", value: "true" };
    expect(nodeToExpressionNode(node, fields)).toEqual({ type: "field", field: "urgent", op: "eq", value: true });
  });

  it("serializes a 'field' leaf with no known field type as a plain string value", () => {
    const node: BuilderNode = { ...emptyLeafNode(), leafType: "field", field: "unknown", op: "eq", value: "x" };
    expect(nodeToExpressionNode(node, fields)).toEqual({ type: "field", field: "unknown", op: "eq", value: "x" });
  });

  it("serializes a 'state' leaf verbatim (no value coercion - all string fields)", () => {
    const node: BuilderNode = {
      ...emptyLeafNode(),
      leafType: "state",
      definitionKey: "purchase_order",
      instanceIdField: "purchaseOrderId",
      state: "received",
    };
    expect(nodeToExpressionNode(node, fields)).toEqual({
      type: "state",
      definitionKey: "purchase_order",
      instanceIdField: "purchaseOrderId",
      state: "received",
    });
  });

  it("serializes a 'field_compare' leaf", () => {
    const node: BuilderNode = {
      ...emptyLeafNode(),
      leafType: "field_compare",
      fieldA: "quantity",
      op: "gt",
      fieldB: "reorderLevel",
    };
    expect(nodeToExpressionNode(node, fields)).toEqual({ type: "field_compare", fieldA: "quantity", op: "gt", fieldB: "reorderLevel" });
  });

  it("serializes a logic node recursively", () => {
    const leaf1: BuilderNode = { ...emptyLeafNode(), leafType: "field", field: "quantity", op: "gt", value: "5" };
    const leaf2: BuilderNode = { ...emptyLeafNode(), leafType: "field", field: "urgent", op: "eq", value: "true" };
    const tree = emptyLogicNode("and", [leaf1, leaf2]);

    expect(nodeToExpressionNode(tree, fields)).toEqual({
      op: "and",
      children: [
        { type: "field", field: "quantity", op: "gt", value: 5 },
        { type: "field", field: "urgent", op: "eq", value: true },
      ],
    });
  });

  it("serializes nested logic nodes (AND wrapping a NOT wrapping a leaf)", () => {
    const inner: BuilderNode = { ...emptyLeafNode(), leafType: "field", field: "item", op: "eq", value: "widget" };
    const notNode = emptyLogicNode("not", [inner]);
    const tree = emptyLogicNode("and", [notNode]);

    expect(nodeToExpressionNode(tree, fields)).toEqual({
      op: "and",
      children: [{ op: "not", children: [{ type: "field", field: "item", op: "eq", value: "widget" }] }],
    });
  });
});

describe("tree mutation helpers", () => {
  it("updateNodeById patches only the matching node, leaving siblings untouched", () => {
    const leaf1: BuilderNode = { ...emptyLeafNode(), field: "a" };
    const leaf2: BuilderNode = { ...emptyLeafNode(), field: "b" };
    const tree = emptyLogicNode("and", [leaf1, leaf2]);

    const updated = updateNodeById(tree, leaf1.id, { field: "changed" });

    expect(updated.kind).toBe("logic");
    expect(updated.children[0].field).toBe("changed");
    expect(updated.children[1].field).toBe("b");
  });

  it("removeNodeById removes only the matching descendant", () => {
    const leaf1: BuilderNode = { ...emptyLeafNode(), field: "a" };
    const leaf2: BuilderNode = { ...emptyLeafNode(), field: "b" };
    const tree = emptyLogicNode("and", [leaf1, leaf2]);

    const updated = removeNodeById(tree, leaf1.id);

    expect(updated.children).toHaveLength(1);
    expect(updated.children[0].field).toBe("b");
  });

  it("addChildToLogicNode appends a fresh leaf to the matching logic node", () => {
    const tree = emptyLogicNode("and", []);
    const updated = addChildToLogicNode(tree, tree.id);
    expect(updated.children).toHaveLength(1);
    expect(updated.children[0].kind).toBe("leaf");
  });

  it("wrapNodeInGroup replaces a node with a new logic group containing a copy of it plus a fresh leaf", () => {
    const leaf = emptyLeafNode();
    const wrapped = wrapNodeInGroup(leaf, leaf.id, "or");

    expect(wrapped.kind).toBe("logic");
    expect(wrapped.logicOp).toBe("or");
    expect(wrapped.children).toHaveLength(2);
    expect(wrapped.children[0].id).toBe(leaf.id);
  });

  it("mutation helpers work through nested trees, not just at the top level", () => {
    const deep = emptyLeafNode();
    const middle = emptyLogicNode("not", [deep]);
    const tree = emptyLogicNode("and", [middle]);

    const updated = updateNodeById(tree, deep.id, { field: "found-me" });
    expect(updated.children[0].children[0].field).toBe("found-me");
  });
});

describe("actionRowToRuleAction", () => {
  it("serializes a transition action", () => {
    const row = { ...emptyActionRow(), type: "transition" as const, actionKey: "send" };
    expect(actionRowToRuleAction(row, fields)).toEqual({ type: "transition", actionKey: "send" });
  });

  it("serializes a notify action", () => {
    const row = { ...emptyActionRow(), type: "notify" as const, messageTemplate: "hi {{item}}" };
    expect(actionRowToRuleAction(row, fields)).toEqual({ type: "notify", channel: "audit_log", messageTemplate: "hi {{item}}" });
  });

  it("serializes a set_field action, coercing its value by the target field's schema type", () => {
    const row = { ...emptyActionRow(), type: "set_field" as const, field: "quantity", value: "42" };
    expect(actionRowToRuleAction(row, fields)).toEqual({ type: "set_field", field: "quantity", value: 42 });
  });
});
