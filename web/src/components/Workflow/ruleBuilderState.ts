// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type {
  ActionType,
  CompareOp,
  ConditionType,
  ExpressionNode,
  FieldSpecInput,
  FieldType,
  LogicOp,
  NotifyChannel,
  RuleAction,
} from "@/lib/workflow";

// RuleBuilder's UI-side condition-tree node - a superset of
// ExpressionNode's own fields (rules.go's flat-struct shape, mirrored by
// lib/workflow.ts's own ExpressionNode) plus a `kind`/`id` the UI needs
// for rendering/editing that never gets serialized. Values are always
// kept as raw strings in UI state (an <input> can only ever produce a
// string) - coerceValue below converts to the field's real schema type
// only at serialization time, the same "UI state is stringly typed until
// submit" pattern DefinitionBuilder.tsx's own BuilderFieldRow uses for
// enum options.
let nextNodeId = 0;
export function freshNodeId(): number {
  return nextNodeId++;
}

export type BuilderNode = {
  id: number;
  kind: "logic" | "leaf";
  // logic node fields
  logicOp: LogicOp;
  children: BuilderNode[];
  // leaf fields
  leafType: ConditionType;
  field: string;
  op: CompareOp;
  value: string;
  definitionKey: string;
  instanceIdField: string;
  state: string;
  fieldA: string;
  fieldB: string;
};

export function emptyLeafNode(): BuilderNode {
  return {
    id: freshNodeId(),
    kind: "leaf",
    logicOp: "and",
    children: [],
    leafType: "field",
    field: "",
    op: "eq",
    value: "",
    definitionKey: "",
    instanceIdField: "",
    state: "",
    fieldA: "",
    fieldB: "",
  };
}

export function emptyLogicNode(op: LogicOp, children: BuilderNode[]): BuilderNode {
  return {
    id: freshNodeId(),
    kind: "logic",
    logicOp: op,
    children,
    leafType: "field",
    field: "",
    op: "eq",
    value: "",
    definitionKey: "",
    instanceIdField: "",
    state: "",
    fieldA: "",
    fieldB: "",
  };
}

/**
 * Looks up a declared payload field's schema FieldType by name - used to
 * decide how a "field"/"set_field" value input renders (text/number/
 * checkbox/enum dropdown) and how its raw string value is coerced at
 * serialization time. Returns undefined for a schema-less definition or
 * an unrecognized field name - callers fall back to a plain text input
 * and string value in that case, same as CreateInstanceForm's own
 * free-form fallback for schema-less definitions.
 */
export function lookupFieldType(fieldName: string, fields: FieldSpecInput[] | undefined): FieldType | undefined {
  return fields?.find((f) => f.name === fieldName)?.type;
}

/**
 * Converts a UI text-input's raw string value into the typed value
 * ExpressionNode.value/RuleAction.value actually needs, based on the
 * target field's declared FieldType (when known - see lookupFieldType).
 * "number" parses to a real number (falling back to the raw string if it
 * doesn't parse, rather than silently sending 0/NaN - the backend's own
 * validation catches a genuinely bad value); "boolean" maps the string
 * "true" to the real boolean true, anything else to false; every other
 * type (including no known type at all) stays a plain string.
 */
export function coerceValue(raw: string, fieldType: FieldType | undefined): unknown {
  if (fieldType === "number") {
    const n = Number(raw);
    return Number.isNaN(n) || raw.trim() === "" ? raw : n;
  }
  if (fieldType === "boolean") {
    return raw === "true";
  }
  return raw;
}

/**
 * nodeToExpressionNode is RuleBuilder's core serialization logic: turns
 * one BuilderNode (and, recursively, its children) into the
 * ExpressionNode tree the backend's CreateRuleForFirm actually accepts.
 * Deliberately a pure function taking only plain data (no React state, no
 * DOM) so it can be unit-tested independently of rendering - see
 * ruleBuilderState.test.ts.
 */
export function nodeToExpressionNode(node: BuilderNode, fields: FieldSpecInput[] | undefined): ExpressionNode {
  if (node.kind === "logic") {
    return {
      op: node.logicOp,
      children: node.children.map((c) => nodeToExpressionNode(c, fields)),
    };
  }

  switch (node.leafType) {
    case "field":
      return {
        type: "field",
        field: node.field,
        op: node.op,
        value: coerceValue(node.value, lookupFieldType(node.field, fields)),
      };
    case "state":
      return {
        type: "state",
        definitionKey: node.definitionKey,
        instanceIdField: node.instanceIdField,
        state: node.state,
      };
    case "field_compare":
      return {
        type: "field_compare",
        fieldA: node.fieldA,
        op: node.op,
        fieldB: node.fieldB,
      };
  }
}

// --- Tree mutation helpers (immutable, id-addressed) ---------------------
//
// The condition tree is edited by id rather than by array index/path,
// since a node's position can change as siblings are added/removed -
// every mutation below walks the tree once, replacing/removing/inserting
// at the matching id and returning a NEW tree (React state must never be
// mutated in place).

export function updateNodeById(
  tree: BuilderNode,
  id: number,
  patch: Partial<BuilderNode>,
): BuilderNode {
  if (tree.id === id) {
    return { ...tree, ...patch };
  }
  if (tree.kind !== "logic") return tree;
  return { ...tree, children: tree.children.map((c) => updateNodeById(c, id, patch)) };
}

/** Removes the node with the given id from tree's descendants - a no-op if id names the root itself (the root is never removable; the caller offers no remove button for it). */
export function removeNodeById(tree: BuilderNode, id: number): BuilderNode {
  if (tree.kind !== "logic") return tree;
  return {
    ...tree,
    children: tree.children.filter((c) => c.id !== id).map((c) => removeNodeById(c, id)),
  };
}

/** Appends a fresh leaf as a new child of the logic node with the given id. */
export function addChildToLogicNode(tree: BuilderNode, id: number): BuilderNode {
  if (tree.id === id && tree.kind === "logic") {
    return { ...tree, children: [...tree.children, emptyLeafNode()] };
  }
  if (tree.kind !== "logic") return tree;
  return { ...tree, children: tree.children.map((c) => addChildToLogicNode(c, id)) };
}

/** Replaces the node with the given id with a new AND-group wrapping a copy of itself plus one fresh leaf sibling - the builder's "add a nested group here" action. */
export function wrapNodeInGroup(tree: BuilderNode, id: number, op: LogicOp): BuilderNode {
  if (tree.id === id) {
    return emptyLogicNode(op, [tree, emptyLeafNode()]);
  }
  if (tree.kind !== "logic") return tree;
  return { ...tree, children: tree.children.map((c) => wrapNodeInGroup(c, id, op)) };
}

// --- Actions ---------------------------------------------------------------

export type BuilderActionRow = {
  id: number;
  type: ActionType;
  actionKey: string;
  channel: NotifyChannel;
  messageTemplate: string;
  field: string;
  value: string;
};

export function emptyActionRow(): BuilderActionRow {
  return {
    id: freshNodeId(),
    type: "transition",
    actionKey: "",
    channel: "audit_log",
    messageTemplate: "",
    field: "",
    value: "",
  };
}

/** actionRowToRuleAction is the action list's own serialization step, same pure/testable shape as nodeToExpressionNode. */
export function actionRowToRuleAction(row: BuilderActionRow, fields: FieldSpecInput[] | undefined): RuleAction {
  switch (row.type) {
    case "transition":
      return { type: "transition", actionKey: row.actionKey };
    case "notify":
      return { type: "notify", channel: row.channel, messageTemplate: row.messageTemplate };
    case "set_field":
      return {
        type: "set_field",
        field: row.field,
        value: coerceValue(row.value, lookupFieldType(row.field, fields)),
      };
  }
}
