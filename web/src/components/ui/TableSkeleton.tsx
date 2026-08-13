// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

type Props = {
  columns: number;
  rows?: number;
};

// Loading skeleton for the list-page pattern (item 4), matching the
// actual table's column count via `columns` - used from each list
// route's own loading.tsx (Next.js's App Router convention, rendered
// automatically during server-side navigation while the page's data
// fetch is in flight) rather than a client spinner, so the abrupt
// pop-in these Server-rendered-then-hydrated pages had before this
// batch is replaced by a shaped placeholder instead.
export default function TableSkeleton({ columns, rows = 6 }: Props) {
  return (
    <div className="flex w-full max-w-4xl flex-col gap-6 px-6 py-16" aria-hidden="true">
      <div className="flex items-center justify-between">
        <div className="h-6 w-40 animate-pulse rounded bg-zinc-200 dark:bg-zinc-800" />
        <div className="h-8 w-28 animate-pulse rounded bg-zinc-200 dark:bg-zinc-800" />
      </div>
      <div className="panel overflow-hidden p-0">
        <table className="w-full border-collapse text-left text-sm">
          <tbody>
            {Array.from({ length: rows }).map((_, r) => (
              <tr key={r} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                {Array.from({ length: columns }).map((_, c) => (
                  <td key={c} className="px-4 py-3">
                    <div className="h-3 w-full max-w-32 animate-pulse rounded bg-zinc-200 dark:bg-zinc-800" />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
