// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { FieldSpecInput } from "@/lib/workflow";

// CreateInstanceForm.tsx's payload-building logic, pulled into its own
// pure/testable module - the same separation format.ts/history.ts already
// use for WorkflowInstanceList/WorkflowHistory's own non-trivial logic,
// so Open Points item 35's schema-vs-freeform branching can be unit
// tested directly, without needing to render the (next-intl/next/navigation
// - dependent) form component itself.

export type FreeformFieldRow = { key: string; value: string };

// hasSchema is CreateInstanceForm's single source of truth for which
// branch to render: typed fields when the target definition's Fields is
// present and non-empty, the freeform key/value editor otherwise -
// mirrors DefinitionSpec.Fields' own "nil/empty means no schema" contract
// (internal/workflow/spec.go) exactly.
export function hasSchema(fields: FieldSpecInput[] | undefined): boolean {
  return Boolean(fields && fields.length > 0);
}

// typedFieldDefault is a schema'd field's initial form value: an empty
// string for text/number/date inputs, false for a checkbox.
export function typedFieldDefault(field: FieldSpecInput): string | boolean {
  return field.type === "boolean" ? false : "";
}

// buildFreeformPayload mirrors this component's pre-schema behavior
// exactly: an empty key is skipped, a value that parses as a finite
// number is stored as a number, everything else as a string.
export function buildFreeformPayload(rows: FreeformFieldRow[]): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (key === "") continue;
    const numeric = Number(row.value);
    payload[key] =
      row.value.trim() !== "" && Number.isFinite(numeric) ? numeric : row.value;
  }
  return payload;
}

// buildTypedPayload converts a schema'd form's values into the
// map[string]any shape CreateInstance expects, per each field's declared
// type - a client-side nice-to-have on top of, not a replacement for, the
// backend's own server-side schema validation
// (internal/workflow.validatePayload). Returns null when a required
// field is missing/invalid, so the caller can show one summary error
// instead of submitting a payload the server would reject anyway.
export function buildTypedPayload(
  fields: FieldSpecInput[],
  values: Record<string, string | boolean>,
): Record<string, unknown> | null {
  const payload: Record<string, unknown> = {};
  for (const field of fields) {
    const raw = values[field.name];
    if (field.type === "boolean") {
      payload[field.name] = Boolean(raw);
      continue;
    }
    const text = typeof raw === "string" ? raw.trim() : "";
    if (text === "") {
      if (field.required) return null;
      continue;
    }
    if (field.type === "number") {
      const numeric = Number(text);
      if (!Number.isFinite(numeric)) return null;
      payload[field.name] = numeric;
    } else {
      // string and date both travel as plain strings - date as whatever
      // an <input type="date"> gives (YYYY-MM-DD), matching the
      // backend's own payloadDateLayout (internal/workflow/engine.go).
      payload[field.name] = text;
    }
  }
  return payload;
}
