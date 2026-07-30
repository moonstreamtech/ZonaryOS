// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { requestOrigin } from "@/lib/requestOrigin";

export async function GET(request: NextRequest) {
  const response = NextResponse.redirect(`${requestOrigin(request)}/`);
  response.cookies.delete("zonaryos_session");
  return response;
}
