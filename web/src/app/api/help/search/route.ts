// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { searchHelpArticles } from "@/lib/helpArticles";

// Proxies the Go backend's GET /api/help/search?q=&locale=.
export async function GET(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const url = new URL(request.url);
  const q = url.searchParams.get("q") ?? "";
  const locale = url.searchParams.get("locale") ?? "en";

  const articles = await searchHelpArticles(token, q, locale);
  if (articles === null) {
    return NextResponse.json({ error: "failed to search help articles" }, { status: 502 });
  }
  return NextResponse.json(articles);
}
