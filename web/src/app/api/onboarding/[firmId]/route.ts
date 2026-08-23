// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { dismissOnboarding, fetchOnboardingProgress } from "@/lib/onboarding";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET/PATCH /api/firms/{firmId}/onboarding - no
// authorization decision made here, same convention as every other
// mutation proxy route in this codebase (internal/onboarding's own
// permission.IsMember check is the sole gate).
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { firmId } = await params;
  const progress = await fetchOnboardingProgress(token, firmId);
  if (!progress) {
    return NextResponse.json({ error: "not found" }, { status: 404 });
  }
  return NextResponse.json(progress);
}

export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { firmId } = await params;
  const progress = await dismissOnboarding(token, firmId);
  if (!progress) {
    return NextResponse.json({ error: "not found" }, { status: 404 });
  }
  return NextResponse.json(progress);
}
