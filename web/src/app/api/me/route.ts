import { NextRequest, NextResponse } from "next/server";
import { fetchMe } from "@/lib/me";

export async function GET(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ authenticated: false }, { status: 401 });
  }

  const me = await fetchMe(token);
  if (!me) {
    return NextResponse.json({ authenticated: false }, { status: 401 });
  }

  return NextResponse.json({ authenticated: true, ...me });
}
