import { NextRequest, NextResponse } from "next/server";

// Proxies the Go backend's GET /api/firms/{firmId}/permission-events
// (Server-Sent Events) - the streaming counterpart of the cookie-to-
// Bearer-token pattern every other proxy route uses. EventSource can't
// set an Authorization header itself, so this route reads the httpOnly
// session cookie (never exposed to client-side JS) and pipes the
// backend's event stream straight through as this response's body.
export async function GET(
  request: NextRequest,
  { params }: { params: Promise<{ firmId: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const apiBase = process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";

  const backendResponse = await fetch(
    `${apiBase}/api/firms/${encodeURIComponent(firmId)}/permission-events`,
    {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
      // @ts-expect-error -- Node's fetch requires this for a streaming
      // request/response pair even though this is a GET with no body;
      // Next.js's fetch types don't declare it.
      duplex: "half",
    },
  );

  if (!backendResponse.ok || !backendResponse.body) {
    return NextResponse.json(
      { error: "event stream unavailable" },
      { status: backendResponse.status || 502 },
    );
  }

  return new NextResponse(backendResponse.body, {
    status: 200,
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
    },
  });
}
