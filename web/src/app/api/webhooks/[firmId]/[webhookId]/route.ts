// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateWebhook, deleteWebhook, type WebhookInput } from "@/lib/webhooks";

type RouteParams = {
  params: Promise<{ firmId: string; webhookId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/webhooks/{webhookId}
// (owner-only). No authorization decision made here, same convention as
// the sibling create route.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, webhookId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<WebhookInput>;
  if (!body.name || !body.url) {
    return NextResponse.json({ error: "missing name or url" }, { status: 400 });
  }

  const result = await updateWebhook(token, firmId, webhookId, {
    name: body.name,
    url: body.url,
    events: body.events ?? [],
    isActive: body.isActive ?? true,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.webhook);
}

// Proxies the Go backend's DELETE /api/firms/{firmId}/webhooks/{webhookId}
// (owner-only, hard delete).
export async function DELETE(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, webhookId } = await params;
  const ok = await deleteWebhook(token, firmId, webhookId);
  if (!ok) {
    return NextResponse.json({ error: "failed to delete webhook" }, { status: 400 });
  }
  return NextResponse.json({ ok: true });
}
