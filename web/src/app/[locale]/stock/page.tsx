// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { STOCK_TO_SALE_KEY } from "@/lib/workflow";
import WorkflowDefinitionView from "@/components/Workflow/WorkflowDefinitionView";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// This route predates the generic /workflows/[key] view (item 1) and is
// kept working at its existing URL on purpose - the Developer has it
// bookmarked and already tested against the real deployment. It is now
// a thin wrapper around the same WorkflowDefinitionView any other
// workflow definition renders through, fixed to the well-known
// stock_to_sale key, rather than its own bespoke stock-shaped
// components (the old StockList/AddStockForm/StockHistory - removed in
// favor of this shared, generic component tree). /workflows/stock_to_sale
// renders the identical page through the generic route; this file exists
// only to preserve the shorter, already-known URL.
export default async function StockPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);

  const { sessionToken, firm } = await requireFirmContext(locale);

  return (
    <WorkflowDefinitionView
      sessionToken={sessionToken}
      firmId={firm.firmId}
      workflowKey={STOCK_TO_SALE_KEY}
    />
  );
}
