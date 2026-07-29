import { NextRequest, NextResponse } from "next/server";
import { executeTransition } from "@/lib/workflow";

// Proxies the Go backend's transition endpoint for exactly the "record
// a sale" action - same cookie-to-Bearer-token pattern as
// src/app/api/me/route.ts and src/app/api/wizard/nodes/*/route.ts. No
// authorization decision is made here: the backend's ExecuteTransition
// (internal/workflow) is the sole place that checks whether the caller
// actually holds the record_sale permission (internal/permission.Has) -
// this route only forwards the caller's session token to it, exactly as
// the CLAUDE.md instruction "do not add a new bespoke auth check"
// requires.
export async function POST(
  request: NextRequest,
  { params }: { params: Promise<{ instanceId: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { instanceId } = await params;
  const body = (await request.json().catch(() => ({}))) as {
    firmId?: string;
  };
  if (!body.firmId) {
    return NextResponse.json({ error: "missing firmId" }, { status: 400 });
  }

  const result = await executeTransition(
    token,
    body.firmId,
    instanceId,
    "record_sale",
  );
  if (!result.ok) {
    return NextResponse.json(
      { error: result.error },
      { status: result.status || 400 },
    );
  }

  return NextResponse.json(result.instance);
}
