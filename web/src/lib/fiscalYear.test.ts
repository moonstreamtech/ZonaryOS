// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import { getCurrentFiscalYearRange, getFiscalYearStart } from "./fiscalYear";

// Part 4's own explicit test requirement: a firm with
// fiscal_year_start_month=4 gets April 1 as the default period start.

describe("getFiscalYearStart", () => {
  it("returns April 1 of the current calendar year when the reference date is after April", () => {
    const start = getFiscalYearStart(4, new Date(Date.UTC(2026, 5, 15))); // June 2026
    expect(start.toISOString()).toBe("2026-04-01T00:00:00.000Z");
  });

  it("returns April 1 of the PREVIOUS calendar year when the reference date is before April", () => {
    const start = getFiscalYearStart(4, new Date(Date.UTC(2026, 1, 10))); // February 2026
    expect(start.toISOString()).toBe("2025-04-01T00:00:00.000Z");
  });

  it("returns January 1 for a calendar-year fiscal year (month=1)", () => {
    const start = getFiscalYearStart(1, new Date(Date.UTC(2026, 5, 15)));
    expect(start.toISOString()).toBe("2026-01-01T00:00:00.000Z");
  });

  it("falls back to January (month 1) for an out-of-range month", () => {
    const start = getFiscalYearStart(13, new Date(Date.UTC(2026, 5, 15)));
    expect(start.getUTCMonth()).toBe(0);
  });
});

describe("getCurrentFiscalYearRange", () => {
  it("returns a full year window starting at the fiscal year start", () => {
    const { start, end } = getCurrentFiscalYearRange(4, new Date(Date.UTC(2026, 5, 15)));
    expect(start.toISOString()).toBe("2026-04-01T00:00:00.000Z");
    expect(end.toISOString()).toBe("2027-04-01T00:00:00.000Z");
  });
});
