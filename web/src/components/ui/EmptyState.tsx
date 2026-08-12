// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { ReactNode } from "react";

type Props = {
  message: string;
  action?: ReactNode;
};

// Shared empty-state block for the list-page pattern (item 4): a simple
// inline SVG illustration (no external image, per the batch brief) plus
// the page's own existing "no items yet" copy and create CTA, passed
// through as-is rather than reworded here.
export default function EmptyState({ message, action }: Props) {
  return (
    <div className="flex flex-col items-center gap-3 py-10 text-center">
      <svg viewBox="0 0 64 64" className="h-12 w-12 text-zinc-300 dark:text-zinc-700" aria-hidden="true">
        <rect x="10" y="18" width="44" height="34" rx="3" fill="none" stroke="currentColor" strokeWidth="2" />
        <path d="M10 26 H54" stroke="currentColor" strokeWidth="2" />
        <path d="M20 34 H40 M20 41 H32" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        <circle cx="46" cy="46" r="9" fill="none" stroke="currentColor" strokeWidth="2" />
        <path d="M43 46 H49 M46 43 V49" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      </svg>
      <p className="text-zinc-600 dark:text-zinc-400">{message}</p>
      {action}
    </div>
  );
}
