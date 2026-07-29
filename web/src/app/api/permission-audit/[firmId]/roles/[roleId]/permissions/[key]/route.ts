import { NextRequest, NextResponse } from "next/server";
import { grantPermission, revokePermission } from "@/lib/permissionAudit";

type RouteParams = {
  params: Promise<{ firmId: string; roleId: string; key: string }>;
};

// Proxies the Go backend's PUT/DELETE .../roles/{roleId}/permissions/{key}
// - same cookie-to-Bearer-token pattern as every other proxy route. No
// authorization decision is made here: the backend's GrantPermission/
// RevokePermission (internal/permission) are the sole place that checks
// the caller actually holds an owner-flagged role - this route only
// forwards the session token to them.
export async function PUT(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, roleId, key } = await params;
  const result = await grantPermission(token, firmId, roleId, key);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return new NextResponse(null, { status: 204 });
}

export async function DELETE(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, roleId, key } = await params;
  const result = await revokePermission(token, firmId, roleId, key);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return new NextResponse(null, { status: 204 });
}
