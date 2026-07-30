// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import type { MeFirm, MeResponse } from "@/lib/me";

// Vision §3's many-firms-per-user model is real (see MeResponse.firms),
// but until this PR every server page picked me.firms[0] unconditionally
// - a known, explicitly-flagged gap (see stock/page.tsx's prior comment
// and layout.tsx's resolveAuditModeContext). This module replaces that
// simplifying assumption everywhere it appeared with a real "which firm
// is the caller currently working in" choice, persisted across requests
// via a cookie (see ACTIVE_FIRM_COOKIE) and set by the firm switcher (see
// components/Nav/FirmSwitcher.tsx and app/api/firm/switch/route.ts).

export const ACTIVE_FIRM_COOKIE = "zonaryos_active_firm";

/**
 * Pure selection logic, split out from resolveActiveFirm below so it can
 * be unit-tested without next/headers' server-only cookies() API: picks
 * requestedFirmId out of me.firms if it names a real membership, else
 * falls back to the first membership (me.firms is assumed non-empty -
 * every caller of this module already redirects a zero-firm user into
 * the wizard before reaching here, same precondition the old
 * me.firms[0] call sites already carried).
 */
export function pickActiveFirm(
  me: MeResponse,
  requestedFirmId: string | null | undefined,
): MeFirm {
  if (requestedFirmId) {
    const match = me.firms.find((f) => f.firmId === requestedFirmId);
    if (match) return match;
  }
  return me.firms[0];
}

/**
 * Server-side helper for page/layout components: reads the active-firm
 * cookie (set by the firm switcher) and resolves it against me.firms,
 * falling back to the first membership when the cookie is missing or
 * names a firm the caller no longer belongs to (e.g. a stale cookie from
 * a membership that was since removed) - never trusting the cookie value
 * on its own, since it's only a UI preference, not an authorization
 * decision (every backend call this firmId feeds into is independently
 * membership-checked, per docs/DEVELOPMENT.md's Authorization checklist).
 */
export async function resolveActiveFirm(me: MeResponse): Promise<MeFirm> {
  const requested = (await cookies()).get(ACTIVE_FIRM_COOKIE)?.value;
  return pickActiveFirm(me, requested);
}
