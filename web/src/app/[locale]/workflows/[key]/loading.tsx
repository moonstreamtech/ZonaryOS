// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import TableSkeleton from "@/components/ui/TableSkeleton";

// Next.js App Router loading state for /workflows/[key] - matches
// WorkflowInstanceList's 4-column table (columnPayload/columnState/
// columnActions/detail-link column).
export default function Loading() {
  return <TableSkeleton columns={4} />;
}
