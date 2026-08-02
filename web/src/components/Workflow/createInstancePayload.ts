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

// ArrayRowValue is one element's form value in an "array" typed field's
// repeatable input group (Open Points item 38) - a string for a
// string/number/date item type (converted at build time, see
// buildTypedPayload), a boolean for a boolean item type (a checkbox row).
export type ArrayRowValue = string | boolean;

// TypedFieldValue is the typed-branch form state's value shape per field:
// a plain string for string/number/date/enum/reference, a boolean for
// boolean, or an array of ArrayRowValue for array - one union covering
// every one of FieldSpecInput's seven types.
export type TypedFieldValue = string | boolean | ArrayRowValue[];

// hasSchema is CreateInstanceForm's single source of truth for which
// branch to render: typed fields when the target definition's Fields is
// present and non-empty, the freeform key/value editor otherwise -
// mirrors DefinitionSpec.Fields' own "nil/empty means no schema" contract
// (internal/workflow/spec.go) exactly.
export function hasSchema(fields: FieldSpecInput[] | undefined): boolean {
  return Boolean(fields && fields.length > 0);
}

// typedFieldDefault is a schema'd field's initial form value: an empty
// string for text/number/date/enum/reference inputs (an enum field's
// select starts on its own empty "not selected yet" option - see
// CreateInstanceForm.tsx - rather than defaulting to Options[0], so a
// required enum field genuinely forces a deliberate choice), false for a
// checkbox, and an empty array for an "array" field's repeatable group.
export function typedFieldDefault(field: FieldSpecInput): TypedFieldValue {
  if (field.type === "boolean") return false;
  if (field.type === "array") return [];
  return "";
}

// arrayItemDefault is one new row's initial value when "add row" is
// clicked on an array field - false for a boolean item type (a checkbox
// row), empty string otherwise.
export function arrayItemDefault(itemType: FieldSpecInput["arrayItemType"]): ArrayRowValue {
  return itemType === "boolean" ? false : "";
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
//
// Open Points item 38's three additions: "enum" travels as a plain string
// (validated client-side against field.options, the same "nice-to-have,
// not a replacement" role as every other type's check here - the backend
// re-checks membership regardless); "reference" travels as a plain string
// too (the picked instance's ID - see CreateInstanceForm.tsx's picker,
// which reuses fetchInstancesPage/searchAcrossDefinitions the same way
// this file already only builds the payload, never fetches); "array"
// converts each row per field.arrayItemType, the same per-type logic this
// function already applies to a single scalar field, just looped.
export function buildTypedPayload(
  fields: FieldSpecInput[],
  values: Record<string, TypedFieldValue>,
): Record<string, unknown> | null {
  const payload: Record<string, unknown> = {};
  for (const field of fields) {
    const raw = values[field.name];
    if (field.type === "boolean") {
      payload[field.name] = Boolean(raw);
      continue;
    }
    if (field.type === "array") {
      const rows = Array.isArray(raw) ? raw : [];
      if (rows.length === 0) {
        if (field.required) return null;
        continue;
      }
      const itemType = field.arrayItemType ?? "string";
      const items: unknown[] = [];
      for (const row of rows) {
        if (itemType === "boolean") {
          items.push(Boolean(row));
          continue;
        }
        const text = typeof row === "string" ? row.trim() : "";
        // An empty row within an otherwise-filled-in array is treated as
        // invalid (not silently dropped) - a caller who added a row and
        // left it blank almost certainly meant to fill it in, and
        // silently dropping it would submit a shorter array than what
        // the form visibly shows.
        if (text === "") return null;
        if (itemType === "number") {
          const numeric = Number(text);
          if (!Number.isFinite(numeric)) return null;
          items.push(numeric);
        } else {
          items.push(text);
        }
      }
      payload[field.name] = items;
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
    } else if (field.type === "enum") {
      if (!(field.options ?? []).includes(text)) return null;
      payload[field.name] = text;
    } else {
      // string, date, and reference all travel as plain strings - date as
      // whatever an <input type="date"> gives (YYYY-MM-DD), matching the
      // backend's own payloadDateLayout (internal/workflow/engine.go);
      // reference as the picked instance's UUID string.
      payload[field.name] = text;
    }
  }
  return payload;
}
