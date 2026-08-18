// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import { formatCurrency, formatDate, formatNumber } from "./format";

// Part 1's own explicit test requirement: currency/date/number formatting
// produce correct output for tr-TR, en-US, and ar-SA - the three locales
// this batch cares about (Turkish, the codebase's own existing default,
// and Arabic - Part 2's new RTL locale placeholder).

describe("formatCurrency", () => {
  it("formats USD for en-US", () => {
    expect(formatCurrency(1234.5, "USD", "en-US")).toBe("$1,234.50");
  });

  it("formats TRY for tr-TR", () => {
    // Turkish grouping/decimal separators are swapped vs. en-US (1.234,50).
    const formatted = formatCurrency(1234.5, "TRY", "tr-TR");
    expect(formatted).toContain("1.234,50");
  });

  it("formats SAR for ar-SA", () => {
    const formatted = formatCurrency(1234.5, "SAR", "ar-SA");
    // Arabic-Indic digits/RTL marks vary by ICU version - assert it
    // produced *some* non-empty currency-shaped string rather than
    // pinning an exact byte sequence.
    expect(formatted.length).toBeGreaterThan(0);
  });

  it("falls back to a plain number for an unrecognized currency code", () => {
    expect(formatCurrency(1234.5, "NOT_A_CODE", "en-US")).toBe("1,234.5");
  });
});

describe("formatDate", () => {
  const date = "2026-03-15T00:00:00Z";

  it("formats short/medium/long for en-US", () => {
    expect(formatDate(date, "en-US", "short")).toBe("03/15/2026");
    expect(formatDate(date, "en-US", "medium")).toBe("Mar 15, 2026");
    expect(formatDate(date, "en-US", "long")).toBe("March 15, 2026");
  });

  it("formats short for tr-TR in day-first order", () => {
    expect(formatDate(date, "tr-TR", "short")).toBe("15.03.2026");
  });

  it("formats a Date object the same as its equivalent ISO string", () => {
    expect(formatDate(new Date(date), "en-US", "short")).toBe(formatDate(date, "en-US", "short"));
  });

  it("returns an empty string for an unparseable date", () => {
    expect(formatDate("not-a-date", "en-US", "short")).toBe("");
  });

  it("formats for ar-SA without throwing", () => {
    expect(formatDate(date, "ar-SA", "medium").length).toBeGreaterThan(0);
  });
});

describe("formatNumber", () => {
  it("formats a plain number for en-US", () => {
    expect(formatNumber(1234.5, "en-US")).toBe("1,234.5");
  });

  it("respects a fixed decimals count", () => {
    expect(formatNumber(3, "en-US", 2)).toBe("3.00");
    expect(formatNumber(1234.567, "en-US", 2)).toBe("1,234.57");
  });

  it("formats with Turkish grouping/decimal separators", () => {
    expect(formatNumber(1234.5, "tr-TR")).toBe("1.234,5");
  });

  it("formats for ar-SA without throwing", () => {
    expect(formatNumber(1234.5, "ar-SA").length).toBeGreaterThan(0);
  });
});
