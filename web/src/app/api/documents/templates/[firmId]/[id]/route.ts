// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateDocumentTemplate, type DocumentTemplateInput } from "@/lib/documents";

type RouteParams = {
  params: Promise<{ firmId: string; id: string }>;
};

// Proxies the Go backend's PUT /api/firms/{firmId}/document-templates/{id}
// (owner-only). No authorization decision made here, same convention as
// the sibling create route.
export async function PUT(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, id } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<DocumentTemplateInput>;
  if (!body.name || !body.type || !body.template) {
    return NextResponse.json({ error: "missing name, type, or template" }, { status: 400 });
  }

  const result = await updateDocumentTemplate(token, firmId, id, {
    name: body.name,
    type: body.type,
    template: body.template,
    isDefault: body.isDefault ?? false,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to update document template" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.template);
}
