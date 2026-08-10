// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createDocumentTemplate, type DocumentTemplateInput } from "@/lib/documents";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/document-templates
// (owner-only) - no authorization decision made here, same convention as
// the sibling localization/tax-rates proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<DocumentTemplateInput>;
  if (!body.name || !body.type || !body.template) {
    return NextResponse.json({ error: "missing name, type, or template" }, { status: 400 });
  }

  const result = await createDocumentTemplate(token, firmId, {
    name: body.name,
    type: body.type,
    template: body.template,
    isDefault: body.isDefault ?? false,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to create document template" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.template, { status: 201 });
}
