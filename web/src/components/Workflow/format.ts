// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Renders an instance's payload (or an audit entry's payload delta) as
// "key: value, key: value" - deliberately generic, since the workflow
// engine's payload is a freeform map[string]any with no per-definition
// schema (see internal/workflow.CreateInstance's payload parameter, and
// docs/OPEN_POINTS.md's new "workflow instance payload schema" item).
// Rendering named columns per field (the way the old stock-specific
// StockList hardcoded "Item"/"Quantity" columns) would only work for
// the one payload shape it was written against; this works for any
// shape, including one this codebase has never seen.
export function formatPayload(
  payload: Record<string, unknown> | null | undefined,
): string {
  if (!payload) return "";
  const entries = Object.entries(payload);
  if (entries.length === 0) return "";
  return entries
    .map(([key, value]) => `${key}: ${formatValue(value)}`)
    .join(", ");
}

function formatValue(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
