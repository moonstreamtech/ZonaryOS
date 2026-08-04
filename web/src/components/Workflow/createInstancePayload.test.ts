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

  // Open Points item 38's three additions.
  describe("enum fields", () => {
    const enumFields: FieldSpecInput[] = [
      { name: "priority", type: "enum", required: true, options: ["low", "medium", "high"] },
    ];

    it("accepts a value present in options", () => {
      expect(buildTypedPayload(enumFields, { priority: "medium" })).toEqual({ priority: "medium" });
    });

    it("returns null for a value not in options", () => {
      expect(buildTypedPayload(enumFields, { priority: "urgent" })).toBeNull();
    });

    it("returns null when a required enum field is left unselected", () => {
      expect(buildTypedPayload(enumFields, { priority: "" })).toBeNull();
    });
  });

  describe("reference fields", () => {
    const referenceFields: FieldSpecInput[] = [
      { name: "relatedStock", type: "reference", required: false, referenceDefinitionKey: "stock_to_sale" },
    ];

    it("carries the picked instance ID through as a plain string", () => {
      const id = "11111111-1111-1111-1111-111111111111";
      expect(buildTypedPayload(referenceFields, { relatedStock: id })).toEqual({ relatedStock: id });
    });

    it("omits an optional reference field left unpicked", () => {
      expect(buildTypedPayload(referenceFields, { relatedStock: "" })).toEqual({});
    });
  });

  describe("array fields", () => {
    const arrayOfStrings: FieldSpecInput[] = [
      { name: "tags", type: "array", required: true, arrayItemType: "string" },
    ];
    const arrayOfNumbers: FieldSpecInput[] = [
      { name: "amounts", type: "array", required: false, arrayItemType: "number" },
    ];
    const arrayOfBooleans: FieldSpecInput[] = [
      { name: "flags", type: "array", required: false, arrayItemType: "boolean" },
    ];

    it("builds a string array from row values", () => {
      expect(buildTypedPayload(arrayOfStrings, { tags: ["urgent", "vip"] })).toEqual({
        tags: ["urgent", "vip"],
      });
    });

    it("converts a number array's rows", () => {
      expect(buildTypedPayload(arrayOfNumbers, { amounts: ["1", "2.5"] })).toEqual({
        amounts: [1, 2.5],
      });
    });

    it("converts a boolean array's rows", () => {
      expect(buildTypedPayload(arrayOfBooleans, { flags: [true, false] })).toEqual({
        flags: [true, false],
      });
    });

    it("returns null for a required array field left empty", () => {
      expect(buildTypedPayload(arrayOfStrings, { tags: [] })).toBeNull();
    });

    it("omits an optional array field left empty", () => {
      expect(buildTypedPayload(arrayOfNumbers, { amounts: [] })).toEqual({});
    });

    it("returns null when a number array row isn't a valid number", () => {
      expect(buildTypedPayload(arrayOfNumbers, { amounts: ["1", "not-a-number"] })).toBeNull();
    });

    it("returns null when a string array row is left blank", () => {
      expect(buildTypedPayload(arrayOfStrings, { tags: ["urgent", ""] })).toBeNull();
    });
  });
});
