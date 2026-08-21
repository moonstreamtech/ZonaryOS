// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

export type OnboardingProgress = {
  completedSteps: string[];
  steps: string[];
  dismissedAt?: string;
};

const API_BASE = () => process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/onboarding`. Returns
 * null on any failure (missing/expired token, backend unreachable, not a
 * member) - same convention as lib/me.ts's fetchMe.
 */
export async function fetchOnboardingProgress(
  token: string,
  firmId: string,
): Promise<OnboardingProgress | null> {
  try {
    const res = await fetch(`${API_BASE()}/api/firms/${encodeURIComponent(firmId)}/onboarding`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as OnboardingProgress;
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `PATCH /api/firms/{firmId}/onboarding` to mark
 * the checklist dismissed - the only supported mutation on this endpoint
 * (step completion is automatic, see internal/onboarding.CompleteStep's
 * own doc comment).
 */
export async function dismissOnboarding(
  token: string,
  firmId: string,
): Promise<OnboardingProgress | null> {
  try {
    const res = await fetch(`${API_BASE()}/api/firms/${encodeURIComponent(firmId)}/onboarding`, {
      method: "PATCH",
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as OnboardingProgress;
  } catch {
    return null;
  }
}
