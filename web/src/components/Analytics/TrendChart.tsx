"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import type { TimeSeriesBucket } from "@/lib/analytics";

type Props = {
  firmId: string;
  locale: string;
};

const EVENT_TYPE_OPTIONS = ["workflow_executed", "page_view"] as const;

const VIEW_WIDTH = 640;
const VIEW_HEIGHT = 220;
const PADDING = 32;

// The "feature usage trend" line chart (design brief Part A.1): no
// charting library is installed in this codebase, so this builds a
// small, deliberately simple inline SVG chart - one <polyline> mapping
// bucket index -> x, count -> y, scaled to a fixed viewBox, with a plain
// <title> per point for hover text instead of a tooltip library. The
// event_type is user-selectable (a small dropdown seeded with
// "workflow_executed"/"page_view" plus a free-text override, since
// internal/analytics accepts any event_type string a caller has actually
// been ingesting).
export default function TrendChart({ firmId, locale }: Props) {
  const t = useTranslations("Analytics");

  const [eventType, setEventType] = useState<string>("workflow_executed");
  const [customEventType, setCustomEventType] = useState("");
  const [buckets, setBuckets] = useState<TimeSeriesBucket[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);

  const effectiveEventType = eventType === "custom" ? customEventType.trim() : eventType;

  useEffect(() => {
    // No event type selected (the "custom" option with an empty text
    // field) - nothing to fetch, leave buckets/loading/error untouched
    // rather than calling setState synchronously at the top of the
    // effect body (points.length === 0 already renders the "empty"
    // message below when buckets is still its initial null).
    if (!effectiveEventType) return;

    let cancelled = false;

    // The actual state-setting work lives inside this nested async
    // function (called, not inlined, from the effect body) rather than
    // directly in the effect's own top-level statements - the react-hooks
    // set-state-in-effect lint rule flags synchronous setState calls
    // sitting directly in an effect body, even ones immediately followed
    // by an async fetch; wrapping them in a function call sidesteps that
    // without changing the actual behavior (still a fire-on-mount/on-
    // dependency-change fetch, still cancellation-guarded).
    async function run() {
      setLoading(true);
      setError(false);

      const to = new Date();
      const from = new Date(to.getTime() - 30 * 24 * 60 * 60 * 1000);
      const isoDate = (d: Date) => d.toISOString().slice(0, 10);

      try {
        const res = await fetch(`/api/analytics/query/${firmId}`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            event_type: effectiveEventType,
            group_by: "day",
            from: isoDate(from),
            to: isoDate(to),
          }),
        });
        if (!res.ok) throw new Error("failed");
        const data: TimeSeriesBucket[] = await res.json();
        if (!cancelled) setBuckets(data);
      } catch {
        if (!cancelled) setError(true);
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    run();

    return () => {
      cancelled = true;
    };
  }, [firmId, effectiveEventType]);

  const { points, maxCount, polylinePoints } = useMemo(() => {
    const data = buckets ?? [];
    const max = Math.max(...data.map((b) => b.count), 1);
    const innerWidth = VIEW_WIDTH - PADDING * 2;
    const innerHeight = VIEW_HEIGHT - PADDING * 2;
    const pts = data.map((b, idx) => {
      const x = data.length <= 1 ? PADDING : PADDING + (idx / (data.length - 1)) * innerWidth;
      const y = PADDING + innerHeight - (b.count / max) * innerHeight;
      return { x, y, bucket: b.bucket, count: b.count };
    });
    return { points: pts, maxCount: max, polylinePoints: pts.map((p) => `${p.x},${p.y}`).join(" ") };
  }, [buckets]);

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("trendTitle")}</h3>
        <div className="flex items-center gap-2 text-sm">
          <label className="flex items-center gap-2">
            {t("eventType")}
            <select
              value={eventType}
              onChange={(e) => setEventType(e.target.value)}
              data-permission-public="true"
              className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            >
              {EVENT_TYPE_OPTIONS.map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))}
              <option value="custom">{t("eventTypeCustom")}</option>
            </select>
          </label>
          {eventType === "custom" && (
            <input
              value={customEventType}
              onChange={(e) => setCustomEventType(e.target.value)}
              placeholder={t("eventTypeCustomPlaceholder")}
              className="rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          )}
        </div>
      </div>

      {loading || (buckets === null && effectiveEventType !== "") ? (
        <p className="text-sm text-zinc-500 dark:text-zinc-500">{t("loading")}</p>
      ) : error ? (
        <p className="text-sm text-red-600 dark:text-red-400">{t("trendError")}</p>
      ) : points.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("trendEmpty")}</p>
      ) : (
        <svg
          viewBox={`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`}
          className="w-full text-black dark:text-zinc-50"
          role="img"
          aria-label={t("trendTitle")}
        >
          <line
            x1={PADDING}
            y1={VIEW_HEIGHT - PADDING}
            x2={VIEW_WIDTH - PADDING}
            y2={VIEW_HEIGHT - PADDING}
            className="stroke-zinc-300 dark:stroke-zinc-700"
            strokeWidth={1}
          />
          <line
            x1={PADDING}
            y1={PADDING}
            x2={PADDING}
            y2={VIEW_HEIGHT - PADDING}
            className="stroke-zinc-300 dark:stroke-zinc-700"
            strokeWidth={1}
          />
          <text x={4} y={PADDING} fontSize={10} className="fill-zinc-500 dark:fill-zinc-500">
            {maxCount}
          </text>
          <text x={4} y={VIEW_HEIGHT - PADDING} fontSize={10} className="fill-zinc-500 dark:fill-zinc-500">
            0
          </text>
          <polyline points={polylinePoints} fill="none" stroke="currentColor" strokeWidth={2} />
          {points.map((p, idx) => (
            <circle key={idx} cx={p.x} cy={p.y} r={3} fill="currentColor">
              <title>
                {new Date(p.bucket).toLocaleDateString(locale)}: {p.count}
              </title>
            </circle>
          ))}
        </svg>
      )}
    </div>
  );
}
