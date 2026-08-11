// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createWebhook, type WebhookInput } from "@/lib/webhooks";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/webhooks (owner-only)
// - no authorization or event-vocabulary validation decision made here:
// internal/webhook.CreateWebhook is the sole place that checks the caller
// is an owner and that requested events are all known, same convention as
// every other proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<WebhookInput>;
  if (!body.name || !body.url) {
    return NextResponse.json({ error: "missing name or url" }, { status: 400 });
  }

  const result = await createWebhook(token, firmId, {
    name: body.name,
    url: body.url,
    events: body.events ?? [],
    isActive: body.isActive ?? true,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.webhook, { status: 201 });
}
