// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { ReactNode } from "react";
import { Link } from "@/i18n/navigation";

export type Crumb = {
  label: string;
  href?: string;
};

type Props = {
  title: string;
  breadcrumb?: Crumb[];
  action?: ReactNode;
};

// Shared header for the list-page pattern (item 4 of this batch): title
// + breadcrumb trail + a slot for the page's existing primary-action
// button (e.g. Invoices' "New Invoice" toggle) - applied identically
// across Invoices/Customers/Inventory/Workflow-instances rather than
// each page hand-rolling its own title/button row, per the batch brief's
// "a consistent set of conventions applied identically across four
// pages" fallback (a full generic ListPage wrapper doesn't fit these
// Manager components' own fetch/mutate/render structure - see the batch
// brief). The action itself (its onClick, its data-permission-* tag) is
// entirely the caller's - this component only lays it out.
export default function ListPageHeader({ title, breadcrumb, action }: Props) {
  return (
    <div className="flex flex-col gap-1">
      {/* No aria-label here - a literal "breadcrumb" string would trip
          check-i18n-strings.mjs as user-facing copy, and this repo has
          no i18n key for it yet; the visible crumb links/labels below
          are already translated at the call site. */}
      {breadcrumb && breadcrumb.length > 0 && (
        <nav className="flex items-center gap-1 text-xs text-zinc-500 dark:text-zinc-500">
          {breadcrumb.map((crumb, idx) => (
            <span key={idx} className="flex items-center gap-1">
              {idx > 0 && <span aria-hidden="true">/</span>}
              {crumb.href ? (
                <Link href={crumb.href} data-permission-public="true" className="hover:underline">
                  {crumb.label}
                </Link>
              ) : (
                <span>{crumb.label}</span>
              )}
            </span>
          ))}
        </nav>
      )}
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">{title}</h2>
        {action}
      </div>
    </div>
  );
}
