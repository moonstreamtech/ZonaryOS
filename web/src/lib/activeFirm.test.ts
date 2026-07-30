// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import { pickActiveFirm } from "./activeFirm";
import type { MeResponse } from "./me";

// Item 3: the firm switcher's selection logic, tested independently of
// next/headers' server-only cookies() API (see activeFirm.ts's own doc
// comment on why pickActiveFirm is split out) - same "extract the pure
// logic, test that" pattern components/AuditMode/useAuditBadges.test.ts
// already established for scanInteractiveElements.

const me: MeResponse = {
  subject: "sub-1",
  email: "owner@example.com",
  displayName: "Owner",
  firms: [
    { firmId: "firm-a", firmName: "Firm A", roleId: "role-a" },
    { firmId: "firm-b", firmName: "Firm B", roleId: "role-b" },
  ],
};

describe("pickActiveFirm", () => {
  it("picks the requested firm when it's a real membership", () => {
    expect(pickActiveFirm(me, "firm-b").firmId).toBe("firm-b");
  });

  it("falls back to the first membership when no firm was requested", () => {
    expect(pickActiveFirm(me, null).firmId).toBe("firm-a");
    expect(pickActiveFirm(me, undefined).firmId).toBe("firm-a");
  });

  it("falls back to the first membership when the requested firm isn't a real membership - a stale cookie", () => {
    expect(pickActiveFirm(me, "firm-not-a-member-of").firmId).toBe("firm-a");
  });

  it("falls back to the first membership for an empty-string cookie value", () => {
    expect(pickActiveFirm(me, "").firmId).toBe("firm-a");
  });
});
