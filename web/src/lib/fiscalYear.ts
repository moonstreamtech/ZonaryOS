// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Multi-language UI/localization depth/fiscal compliance batch, Part 4:
// "this fiscal year" date-range defaults, driven by the firm's
// fiscalYearStartMonth (internal/firm's own fiscal hint, sourced from
// fiscal_country_configs.fiscal_year_start_month - see
// internal/firm/handlers.go's applyFiscalHints). Deliberately a single,
// small, framework-free module: the /financials, /reports, and P&L date
// pickers each import getCurrentFiscalYearRange directly rather than
// this logic living inside any one of those components, so all three
// stay in sync without importing from one another.

export type FiscalYearRange = { start: Date; end: Date };

/**
 * Returns the UTC-midnight start-of-day for startMonth (1-12) - the
 * fiscal year boundary. Exported mainly for getCurrentFiscalYearRange
 * below and for direct testing; call sites building a date-range picker
 * normally want getCurrentFiscalYearRange instead.
 */
export function getFiscalYearStart(startMonth: number, referenceDate: Date = new Date()): Date {
  const month = clampMonth(startMonth);
  const year = referenceDate.getUTCFullYear();
  const referenceMonth = referenceDate.getUTCMonth() + 1; // Date.getUTCMonth() is 0-indexed
  // If the reference date falls before this calendar year's own fiscal-
  // year start, the current fiscal year actually began the PREVIOUS
  // calendar year (e.g. a UK firm - fiscal_year_start_month=4 - in
  // January is still in the fiscal year that started the previous
  // April).
  const fiscalYear = referenceMonth >= month ? year : year - 1;
  return new Date(Date.UTC(fiscalYear, month - 1, 1));
}

/**
 * Returns [start, end) of the fiscal year containing referenceDate
 * (default: now) - end is exclusive, exactly one year after start, so a
 * date-range picker can use it directly as `{from: start, to: end}`
 * without an off-by-one adjustment.
 */
export function getCurrentFiscalYearRange(
  startMonth: number,
  referenceDate: Date = new Date(),
): FiscalYearRange {
  const start = getFiscalYearStart(startMonth, referenceDate);
  const end = new Date(Date.UTC(start.getUTCFullYear() + 1, start.getUTCMonth(), 1));
  return { start, end };
}

function clampMonth(month: number): number {
  if (!Number.isInteger(month) || month < 1 || month > 12) return 1;
  return month;
}
