// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchDefinitionByKey, fetchInstancesPage } from "@/lib/workflow";

// Proxies a "list this definition's instances, by well-known key" read,
// for Open Points item 38's reference-field picker
// (CreateInstanceForm.tsx's ReferencePicker): a FieldSpec's
// referenceDefinitionKey is a definition KEY (e.g. "stock_to_sale"), not
// a definitionId, and a client component has no bearer token of its own
// to resolve one to the other itself (only the httpOnly session cookie).
// This route does both steps server-side with that cookie's token, using
// the SAME two already-existing helpers server components already call
// (fetchDefinitionByKey, then fetchInstancesPage - see
// WorkflowDefinitionView.tsx/lib/workflow.ts) rather than a new lookup
// mechanism - it's just the first CLIENT-initiated caller of either.
export async function GET(
  request: NextRequest,
  { params }: { params: Promise<{ key: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { key } = await params;
  const searchParams = request.nextUrl.searchParams;
  const firmId = searchParams.get("firmId");
  if (!firmId) {
    return NextResponse.json({ error: "missing firmId" }, { status: 400 });
  }
  const limit = Number(searchParams.get("limit") ?? "10") || 10;
  const offset = Number(searchParams.get("offset") ?? "0") || 0;
  const q = searchParams.get("q") ?? undefined;

  const definition = await fetchDefinitionByKey(token, firmId, key);
  if (!definition) {
    return NextResponse.json({ error: "not found" }, { status: 404 });
  }

  const page = await fetchInstancesPage(token, firmId, definition.definitionId, { limit, offset, q });
  if (!page) {
    return NextResponse.json({ error: "not available" }, { status: 404 });
  }

  return NextResponse.json(page);
}
