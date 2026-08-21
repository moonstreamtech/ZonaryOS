// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchHelpArticles } from "@/lib/helpArticles";

// Proxies the Go backend's GET /api/help?route=&locale= - no
// authorization decision made here beyond "is there a valid session"
// (internal/helparticles has no firm-scoping at all, see its own doc
// comment).
export async function GET(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const url = new URL(request.url);
  const route = url.searchParams.get("route") ?? "";
  const locale = url.searchParams.get("locale") ?? "en";

  const articles = await fetchHelpArticles(token, route, locale);
  if (articles === null) {
    return NextResponse.json({ error: "failed to load help articles" }, { status: 502 });
  }
  return NextResponse.json(articles);
}
