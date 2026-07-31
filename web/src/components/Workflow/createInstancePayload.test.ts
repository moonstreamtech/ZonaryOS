// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import type { FieldSpecInput } from "@/lib/workflow";
import {
  buildFreeformPayload,
  buildTypedPayload,
  hasSchema,
  typedFieldDefault,
} from "./createInstancePayload";

// Open Points item 35's frontend proof: CreateInstanceForm branches on
// hasSchema() to decide typed-fields vs. freeform-editor rendering
// (CreateInstanceForm.tsx itself isn't unit-rendered here - it depends on
// next-intl/next/navigation context - but its actual branching/payload
// logic lives in this module and is fully covered directly, the same
// separation format.test.ts/history.test.ts already use).

describe("hasSchema", () => {
  it("is false for an undefined fields (schema-less definition, e.g. stock_to_sale today)", () => {
    expect(hasSchema(undefined)).toBe(false);
  });

  it("is false for an empty fields array", () => {
    expect(hasSchema([])).toBe(false);
  });

  it("is true when at least one field is declared", () => {
    const fields: FieldSpecInput[] = [{ name: "item", type: "string", required: true }];
    expect(hasSchema(fields)).toBe(true);
  });
});

describe("typedFieldDefault", () => {
  it("defaults a boolean field to false", () => {
    expect(typedFieldDefault({ name: "rush", type: "boolean", required: false })).toBe(false);
  });

  it("defaults string/number/date fields to an empty string", () => {
    expect(typedFieldDefault({ name: "item", type: "string", required: true })).toBe("");
    expect(typedFieldDefault({ name: "quantity", type: "number", required: true })).toBe("");
    expect(typedFieldDefault({ name: "dueDate", type: "date", required: false })).toBe("");
  });
});

describe("buildFreeformPayload", () => {
  it("matches the pre-schema behavior: skips empty keys, coerces finite numbers", () => {
    const payload = buildFreeformPayload([
      { key: "item", value: "widget" },
      { key: "quantity", value: "10" },
      { key: "", value: "ignored - no key" },
    ]);
    expect(payload).toEqual({ item: "widget", quantity: 10 });
  });

  it("keeps a non-numeric value as a plain string", () => {
    const payload = buildFreeformPayload([{ key: "contact", value: "lead@example.com" }]);
    expect(payload).toEqual({ contact: "lead@example.com" });
  });
});

describe("buildTypedPayload", () => {
  const fields: FieldSpecInput[] = [
    { name: "item", type: "string", required: true },
    { name: "quantity", type: "number", required: true },
    { name: "rush", type: "boolean", required: false },
    { name: "dueDate", type: "date", required: false },
  ];

  it("builds a payload from a fully-valid set of values", () => {
    const payload = buildTypedPayload(fields, {
      item: "widget",
      quantity: "10",
      rush: true,
      dueDate: "2026-08-01",
    });
    expect(payload).toEqual({
      item: "widget",
      quantity: 10,
      rush: true,
      dueDate: "2026-08-01",
    });
  });

  it("omits an optional field left blank rather than erroring", () => {
    const payload = buildTypedPayload(fields, {
      item: "widget",
      quantity: "1",
      rush: false,
      dueDate: "",
    });
    expect(payload).toEqual({ item: "widget", quantity: 1, rush: false });
  });

  it("returns null when a required field is left blank", () => {
    const payload = buildTypedPayload(fields, {
      item: "",
      quantity: "10",
      rush: false,
      dueDate: "",
    });
    expect(payload).toBeNull();
  });

  it("returns null when a required number field isn't a valid number", () => {
    const payload = buildTypedPayload(fields, {
      item: "widget",
      quantity: "ten",
      rush: false,
      dueDate: "",
    });
    expect(payload).toBeNull();
  });
});
