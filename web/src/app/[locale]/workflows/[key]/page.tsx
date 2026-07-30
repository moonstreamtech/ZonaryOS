// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import WorkflowDefinitionView from "@/components/Workflow/WorkflowDefinitionView";

type PageProps = {
  params: Promise<{ locale: string; key: string }>;
};

// Item 1's generic instance view: any workflow definition the firm has,
// addressed by its well-known key (e.g. /workflows/stock_to_sale,
// /workflows/purchase_order, ...), rendered through the same
// WorkflowDefinitionView the /stock route's thin wrapper uses - no new
// frontend code needed for a firm's second, third, ... workflow.
export default async function WorkflowDefinitionPage({ params }: PageProps) {
  const { locale, key } = await params;
  setRequestLocale(locale);

  const { sessionToken, firm } = await requireFirmContext(locale);

  return (
    <WorkflowDefinitionView
      sessionToken={sessionToken}
      firmId={firm.firmId}
      workflowKey={key}
    />
  );
}
