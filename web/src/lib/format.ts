// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Multi-language UI/localization depth/fiscal compliance batch, Part 1:
// a thin, dependency-free formatting layer over the browser/Node's own
// built-in Intl APIs - no new locale-data dependency, since Intl already
// ships full ICU data in every modern Node/browser runtime this project
// targets. Every formatter takes an explicit locale argument rather than
// reading next-intl's own useLocale() internally, so these stay usable
// from both client components and plain data-fetching code (report
// builders, CSV-adjacent display code) without a hook-context dependency.
//
// The locale a caller should normally pass is the FIRM's own
// default_locale (internal/firm.Metadata.DefaultLocale) - a firm's
// invoices/reports/dashboards format numbers the way that firm's own
// country expects, which is not necessarily the viewing user's own UI
// language (next-intl's routing locale). Falling back to the UI locale
// when a firm has no default_locale set is each call site's own choice,
// not something these functions decide for it.

/**
 * Formats amount as a currency value using Intl.NumberFormat, e.g.
 * formatCurrency(1234.5, "USD", "en-US") -> "$1,234.50".
 */
export function formatCurrency(amount: number, currency: string, locale: string): string {
  try {
    return new Intl.NumberFormat(locale, { style: "currency", currency }).format(amount);
  } catch {
    // An unrecognized locale or currency code (e.g. free-text
    // default_currency that was never validated against ISO 4217) falls
    // back to a plain number rather than throwing - a malformed
    // formatting hint should degrade the display, never break the page.
    return new Intl.NumberFormat("en-US", { style: "decimal" }).format(amount);
  }
}

// DateFormatStyle names the three granularities every date-picker/table
// column in this codebase needs - deliberately not exposing
// Intl.DateTimeFormatOptions' full surface, so every call site picks
// from the same three consistent looks rather than inventing its own
// options object per component.
export type DateFormatStyle = "short" | "medium" | "long";

const DATE_FORMAT_OPTIONS: Record<DateFormatStyle, Intl.DateTimeFormatOptions> = {
  short: { year: "numeric", month: "2-digit", day: "2-digit" },
  medium: { year: "numeric", month: "short", day: "numeric" },
  long: { year: "numeric", month: "long", day: "numeric" },
};

/**
 * Formats date using Intl.DateTimeFormat. Accepts either a Date or an
 * ISO-8601 string (every date this codebase's own APIs return over the
 * wire), so a call site never has to remember to wrap `new Date(...)`
 * itself.
 */
export function formatDate(
  date: string | Date,
  locale: string,
  format: DateFormatStyle,
): string {
  const d = typeof date === "string" ? new Date(date) : date;
  if (Number.isNaN(d.getTime())) return "";
  try {
    return new Intl.DateTimeFormat(locale, DATE_FORMAT_OPTIONS[format]).format(d);
  } catch {
    return new Intl.DateTimeFormat("en-US", DATE_FORMAT_OPTIONS[format]).format(d);
  }
}

/**
 * Formats value as a plain (non-currency) number using
 * Intl.NumberFormat - inventory quantities, KPI counts, percentages
 * already multiplied out, etc. decimals fixes both the minimum and
 * maximum fraction digits when given (e.g. `formatNumber(3, "en-US", 2)`
 * -> "3.00"); omitted, Intl's own locale-appropriate default applies.
 */
export function formatNumber(value: number, locale: string, decimals?: number): string {
  const options: Intl.NumberFormatOptions =
    decimals === undefined
      ? {}
      : { minimumFractionDigits: decimals, maximumFractionDigits: decimals };
  try {
    return new Intl.NumberFormat(locale, options).format(value);
  } catch {
    return new Intl.NumberFormat("en-US", options).format(value);
  }
}
