// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";

type RouteParams = {
  params: Promise<{ firmId: string; invoiceId: string }>;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

// Proxies the Go backend's GET /api/firms/{firmId}/invoices/{invoiceId}/render
// and streams the rendered HTML straight back - the invoice detail
// page's "Preview" button opens this URL directly in a new tab
// (cookie-authenticated, same-origin), rather than a client-side fetch,
// since the point is a real rendered HTML document, not JSON.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, invoiceId } = await params;
  const templateId = request.nextUrl.searchParams.get("templateID");
  const url = new URL(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/invoices/${encodeURIComponent(invoiceId)}/render`);
  if (templateId) {
    url.searchParams.set("templateID", templateId);
  }

  try {
    const res = await fetch(url, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    const body = await res.text();
    return new NextResponse(body, {
      status: res.status,
      headers: { "Content-Type": res.headers.get("Content-Type") ?? "text/html; charset=utf-8" },
    });
  } catch {
    return NextResponse.json({ error: "failed to render invoice" }, { status: 502 });
  }
}
