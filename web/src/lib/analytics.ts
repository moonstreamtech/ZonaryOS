// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Analytics/BI/advanced reporting batch, Parts 1-2, frontend side
// (internal/analytics) - mirrors lib/webhooks.ts's own "types + fetch
// helpers, one file per backend package" convention. Every endpoint here
// is member-gated on the backend (see internal/analytics/handlers.go's
// own RegisterRoutes doc comment) - this is ordinary firm data
// visibility, not a structural firm decision, so unlike lib/webhooks.ts
// none of these need an isOwner gate on the frontend either.

export type EventInput = {
  eventType: string;
  entityType?: string;
  entityId?: string;
  properties?: Record<string, unknown>;
  occurredAt?: string;
};

export type TimeSeriesBucket = {
  bucket: string;
  count: number;
};

export type TimeSeriesGroupBy = "hour" | "day" | "week" | "month";

export type TimeSeriesQueryInput = {
  event_type: string;
  group_by: TimeSeriesGroupBy;
  from: string;
  to: string;
};

export type PropertyCount = {
  value: string;
  count: number;
};

export type ActiveUserCount = {
  count: number;
  days: number;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/analytics/events`
 * with a JSON array body (max 100 events per internal/analytics's own
 * maxEventBatchSize) - server-side helper, used by the events proxy
 * route. Client code posts through that proxy route instead of calling
 * this directly (see components/Analytics/PageViewTracker.tsx).
 */
export async function ingestEvents(token: string, firmId: string, events: EventInput[]): Promise<boolean> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/analytics/events`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(events),
      cache: "no-store",
    });
    return res.ok;
  } catch {
    return false;
  }
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/analytics/query` -
 * the feature-usage trend chart's own data source. Returns null on
 * failure, same swallow-to-null convention as lib/webhooks.ts's read
 * helpers.
 */
export async function fetchTimeSeriesQuery(
  token: string,
  firmId: string,
  input: TimeSeriesQueryInput,
): Promise<TimeSeriesBucket[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/analytics/query`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as TimeSeriesBucket[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/analytics/top` - the
 * shared endpoint behind both "top workflows by execution count" and the
 * page-view heatmap table (see internal/analytics/handlers.go's own
 * handleTopByProperty doc comment).
 */
export async function fetchTopByProperty(
  token: string,
  firmId: string,
  eventType: string,
  property: string,
  limit: number,
): Promise<PropertyCount[] | null> {
  try {
    const url = new URL(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/analytics/top`);
    url.searchParams.set("eventType", eventType);
    url.searchParams.set("property", property);
    url.searchParams.set("limit", String(limit));
    const res = await fetch(url, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as PropertyCount[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/analytics/active-users`. */
export async function fetchActiveUserCount(
  token: string,
  firmId: string,
  days: number,
): Promise<ActiveUserCount | null> {
  try {
    const url = new URL(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/analytics/active-users`);
    url.searchParams.set("days", String(days));
    const res = await fetch(url, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as ActiveUserCount;
  } catch {
    return null;
  }
}
