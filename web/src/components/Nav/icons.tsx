// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Small, simple 16px inline SVG icon set for the dark sidebar rework
// (this batch). Icon strategy: per-GROUP icons (not one bespoke icon per
// nav item) - the sidebar has 20+ individual nav items across 5 groups,
// and drawing 20+ distinct, meaningful glyphs is unrealistic scope for
// one batch (the task brief explicitly calls this out as an accepted
// fallback: "keep per-GROUP icons but make them real SVG glyphs instead
// of letter abbreviations"). Every item in a group shares that group's
// icon; the two additional icons below (IconHome, IconLogout, IconSearch)
// are one-offs for destinations that sit outside the 5-group list
// (dashboard, logout, search), not part of the "20+ items" scope problem.
// Same visual convention throughout: 16x16 viewBox, single-color
// `currentColor` stroke, ~1.4-1.5 stroke width, round caps/joins - no
// icon library dependency, matching the stroke weight already
// established by app/[locale]/page.tsx's WorkflowIcon/AuditIcon/
// TrendGlyph (this same batch restyles that page too).

type IconProps = {
  className?: string;
};

const BASE = "shrink-0";

export function IconHome({ className }: IconProps) {
  return (
    <svg viewBox="0 0 16 16" className={`${BASE} ${className ?? ""}`} aria-hidden="true">
      <path
        d="M2 7.5 L8 2.5 L14 7.5 V13 A0.6 0.6 0 0 1 13.4 13.6 H2.6 A0.6 0.6 0 0 1 2 13 Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path d="M6 13.6 V9.5 H10 V13.6" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
    </svg>
  );
}

// Operations group: a stack/cube glyph (stock, workflows, inventory,
// suppliers, logistics, customers, tasks, approvals, edge agents all
// live under this group).
export function IconOperations({ className }: IconProps) {
  return (
    <svg viewBox="0 0 16 16" className={`${BASE} ${className ?? ""}`} aria-hidden="true">
      <path
        d="M8 1.6 L14 4.8 V11.2 L8 14.4 L2 11.2 V4.8 Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <path d="M2 4.8 L8 8 L14 4.8 M8 8 V14.4" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
    </svg>
  );
}

// Finance group: a banknote glyph (financials, invoices, reports, payroll).
export function IconFinance({ className }: IconProps) {
  return (
    <svg viewBox="0 0 16 16" className={`${BASE} ${className ?? ""}`} aria-hidden="true">
      <rect x="1.5" y="4" width="13" height="8" rx="1.2" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <circle cx="8" cy="8" r="2" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <path d="M3.5 4 V12 M12.5 4 V12" stroke="currentColor" strokeWidth="1.1" strokeLinecap="round" />
    </svg>
  );
}

// HR group: a people glyph (people, time tracking, absences).
export function IconHr({ className }: IconProps) {
  return (
    <svg viewBox="0 0 16 16" className={`${BASE} ${className ?? ""}`} aria-hidden="true">
      <circle cx="6" cy="5.2" r="2.2" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path d="M1.8 14 C1.8 10.8 3.6 9.4 6 9.4 C8.4 9.4 10.2 10.8 10.2 14" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <circle cx="11.2" cy="5.6" r="1.7" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <path d="M10.4 9.6 C12.6 9.9 14.2 11.1 14.2 14" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

// Settings group: a gear glyph (accounts, addresses, tax rates, document
// templates, API keys, webhooks, members).
export function IconSettings({ className }: IconProps) {
  return (
    <svg viewBox="0 0 16 16" className={`${BASE} ${className ?? ""}`} aria-hidden="true">
      <circle cx="8" cy="8" r="2.3" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path
        d="M8 1.8 V3.4 M8 12.6 V14.2 M14.2 8 H12.6 M3.4 8 H1.8 M12.4 3.6 L11.3 4.7 M4.7 11.3 L3.6 12.4 M12.4 12.4 L11.3 11.3 M4.7 4.7 L3.6 3.6"
        stroke="currentColor"
        strokeWidth="1.3"
        strokeLinecap="round"
      />
    </svg>
  );
}

// Administration group: a shield glyph (audit log, permissions).
export function IconAdministration({ className }: IconProps) {
  return (
    <svg viewBox="0 0 16 16" className={`${BASE} ${className ?? ""}`} aria-hidden="true">
      <path
        d="M8 1.8 L13.5 3.8 V7.6 C13.5 11 11.2 13.4 8 14.4 C4.8 13.4 2.5 11 2.5 7.6 V3.8 Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <path d="M5.6 8 L7.3 9.7 L10.6 6.2" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function IconSearch({ className }: IconProps) {
  return (
    <svg viewBox="0 0 16 16" className={`${BASE} ${className ?? ""}`} aria-hidden="true">
      <circle cx="7" cy="7" r="4.4" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path d="M10.2 10.2 L14 14" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

export function IconLogout({ className }: IconProps) {
  return (
    <svg viewBox="0 0 16 16" className={`${BASE} ${className ?? ""}`} aria-hidden="true">
      <path d="M6.5 1.8 H3.6 A0.8 0.8 0 0 0 2.8 2.6 V13.4 A0.8 0.8 0 0 0 3.6 14.2 H6.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M9.4 4.6 L13.2 8 L9.4 11.4" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M6 8 H13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}
