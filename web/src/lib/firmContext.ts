// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { fetchMe, type MeFirm, type MeResponse } from "@/lib/me";
import { resolveActiveFirm } from "@/lib/activeFirm";

export type FirmContext = {
  me: MeResponse;
  sessionToken: string;
  firm: MeFirm;
};

// Shared "who is this, which firm are they working in" resolution for
// every firm-scoped server page (the stock page, the generic workflow
// pages, the workflows list, ...) - the same three steps StockPage and
// AuditLogPage already each wrote out by hand, pulled into one place now
// that a third and fourth page need it too (item 1's /workflows and
// /workflows/[key]). redirect() never returns (Next.js types it `never`),
// so TypeScript narrows `me` to non-null for every statement after the
// first check below without an extra assertion.
export async function requireFirmContext(locale: string): Promise<FirmContext> {
  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  const me = sessionToken ? await fetchMe(sessionToken) : null;

  if (!me) {
    redirect(`/${locale}`);
  }
  if (me.firms.length === 0) {
    redirect(`/${locale}/wizard`);
  }

  const firm = await resolveActiveFirm(me);
  return { me, sessionToken: sessionToken!, firm };
}
