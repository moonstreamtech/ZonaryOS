import { NextRequest, NextResponse } from "next/server";
import { postWizardAnswer } from "@/lib/wizard";

// Proxies the Go backend's POST /api/wizard/nodes/{nodeKey}/answer, same
// cookie-to-Bearer-token pattern as the sibling GET route and
// src/app/api/me/route.ts.
export async function POST(
  request: NextRequest,
  { params }: { params: Promise<{ nodeKey: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { nodeKey } = await params;
  const body = (await request.json().catch(() => ({}))) as {
    answer?: string;
    firmName?: string;
  };
  if (!body.answer) {
    return NextResponse.json({ error: "missing answer" }, { status: 400 });
  }

  const result = await postWizardAnswer(
    token,
    nodeKey,
    body.answer,
    body.firmName,
  );
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: 400 });
  }

  return NextResponse.json(result.node);
}
