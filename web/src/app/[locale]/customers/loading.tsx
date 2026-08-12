// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import TableSkeleton from "@/components/ui/TableSkeleton";

// Next.js App Router loading state for /customers - CustomersManager
// renders a card-per-row list, not a literal <table>, but the same
// shaped-placeholder skeleton reads fine for either layout (see
// components/ui/TableSkeleton.tsx's own doc comment).
export default function Loading() {
  return <TableSkeleton columns={2} />;
}
