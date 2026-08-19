// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchTimeSeriesQuery, type TimeSeriesQueryInput } from "@/lib/analytics";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/analytics/query
// (member-gated) - the /analytics page's feature-usage trend chart calls
// this from the browser (components/Analytics/TrendChart.tsx) since the
// event_type it charts is user-selectable client-side. No validation
// decision made here: internal/analytics.TimeSeriesQuery is the sole
// place that checks the group_by vocabulary and date range, same
// convention as every other proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<TimeSeriesQueryInput>;
  if (!body.event_type || !body.group_by || !body.from || !body.to) {
    return NextResponse.json({ error: "missing event_type, group_by, from, or to" }, { status: 400 });
  }

  const buckets = await fetchTimeSeriesQuery(token, firmId, {
    event_type: body.event_type,
    group_by: body.group_by,
    from: body.from,
    to: body.to,
  });
  if (buckets === null) {
    return NextResponse.json({ error: "failed to query analytics" }, { status: 502 });
  }
  return NextResponse.json(buckets);
}
