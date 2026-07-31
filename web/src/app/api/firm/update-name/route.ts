// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateFirm } from "@/lib/firm";

// Proxies the Go backend's PATCH /api/firms/{firmId} - item 4's original
// name-only mutation, extended by Open Points item 36 to also forward the
// five optional broader-metadata fields the settings page now edits. No
// authorization decision is made here: internal/firm.Update is the sole
// place that checks the caller is an owner. Kept at this same route
// (rather than a new one) since it's the same PATCH, just carrying more
// optional fields now - see internal/firm's own doc comment for why this
// stays one endpoint.
export async function PATCH(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const body = (await request.json().catch(() => ({}))) as {
    firmId?: string;
    name?: string;
    address?: string;
    taxId?: string;
    defaultLocale?: string;
    defaultCurrency?: string;
    logoUrl?: string;
  };
  if (!body.firmId || !body.name) {
    return NextResponse.json({ error: "missing firmId or name" }, { status: 400 });
  }

  const fields: {
    address?: string;
    taxId?: string;
    defaultLocale?: string;
    defaultCurrency?: string;
    logoUrl?: string;
  } = {};
  if (body.address !== undefined) fields.address = body.address;
  if (body.taxId !== undefined) fields.taxId = body.taxId;
  if (body.defaultLocale !== undefined) fields.defaultLocale = body.defaultLocale;
  if (body.defaultCurrency !== undefined) fields.defaultCurrency = body.defaultCurrency;
  if (body.logoUrl !== undefined) fields.logoUrl = body.logoUrl;

  const result = await updateFirm(token, body.firmId, body.name, fields);
  if (!result.ok) {
    return NextResponse.json(
      { error: result.error },
      { status: result.status || 400 },
    );
  }

  return NextResponse.json(result.metadata);
}
