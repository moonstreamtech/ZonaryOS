// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import type { MetricsSummary } from "@/lib/perfMetrics";

type Props = {
  summary: MetricsSummary;
};

const CHART_WIDTH = 640;
const CHART_HEIGHT = 180;
const CHART_PADDING = 28;

// The /platform-admin/metrics page's body: per-endpoint p50/p95/p99 +
// error rate table, a top-10-slowest highlight table, and a request-
// volume-by-hour bar chart. No charting library is installed in this
// codebase (confirmed via package.json) - the established convention
// (Analytics/TrendChart.tsx) is a small, hand-rolled inline SVG, so the
// volume chart here follows that same shape (bars instead of a polyline,
// since hourly request counts read more naturally as a bar chart than a
// trend line, but same fixed-viewBox/no-tooltip-library approach). This
// is a plain server-renderable function (no hooks, no "use client") since
// the whole dashboard is read-only with no per-view state.
export default async function MetricsDashboard({ summary }: Props) {
  const t = await getTranslations("PlatformAdminMetrics");

  const maxVolume = Math.max(...summary.volumeByHour.map((b) => b.count), 1);
  const innerWidth = CHART_WIDTH - CHART_PADDING * 2;
  const innerHeight = CHART_HEIGHT - CHART_PADDING * 2;
  const barCount = summary.volumeByHour.length;
  const barWidth = barCount > 0 ? innerWidth / barCount : innerWidth;

  return (
    <div className="flex w-full max-w-4xl flex-col gap-8">
      <section className="rounded-lg border border-indigo-800 bg-black/20 p-4">
        <h2 className="mb-3 text-lg font-semibold text-[var(--color-platform-on-accent)]">
          {t("volumeTitle")}
        </h2>
        {summary.volumeByHour.length === 0 ? (
          <p className="text-sm text-indigo-200">{t("volumeEmpty")}</p>
        ) : (
          <svg
            viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
            className="w-full text-indigo-300"
            role="img"
            aria-label={t("volumeTitle")}
          >
            <line
              x1={CHART_PADDING}
              y1={CHART_HEIGHT - CHART_PADDING}
              x2={CHART_WIDTH - CHART_PADDING}
              y2={CHART_HEIGHT - CHART_PADDING}
              stroke="currentColor"
              strokeWidth={1}
              opacity={0.5}
            />
            <text x={4} y={CHART_PADDING} fontSize={10} fill="currentColor">
              {maxVolume}
            </text>
            <text x={4} y={CHART_HEIGHT - CHART_PADDING} fontSize={10} fill="currentColor">
              0
            </text>
            {summary.volumeByHour.map((bucket, idx) => {
              const barHeight = (bucket.count / maxVolume) * innerHeight;
              const x = CHART_PADDING + idx * barWidth;
              const y = CHART_HEIGHT - CHART_PADDING - barHeight;
              return (
                <rect
                  key={bucket.hour}
                  x={x + barWidth * 0.1}
                  y={y}
                  width={Math.max(barWidth * 0.8, 1)}
                  height={barHeight}
                  fill="currentColor"
                >
                  <title>
                    {new Date(bucket.hour).toLocaleString()}: {bucket.count}
                  </title>
                </rect>
              );
            })}
          </svg>
        )}
      </section>

      <section className="rounded-lg border border-indigo-800 bg-black/20 p-4">
        <h2 className="mb-3 text-lg font-semibold text-[var(--color-platform-on-accent)]">
          {t("topSlowestTitle")}
        </h2>
        {summary.topSlowest.length === 0 ? (
          <p className="text-sm text-indigo-200">{t("empty")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-indigo-800 text-indigo-200">
                  <th className="py-2 pr-4 font-medium">{t("columnEndpoint")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnP95")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnP99")}</th>
                  <th className="py-2 font-medium">{t("columnErrorRate")}</th>
                </tr>
              </thead>
              <tbody>
                {summary.topSlowest.map((ep) => (
                  <tr
                    key={ep.pathPattern}
                    className="border-b border-indigo-900 text-[var(--color-platform-on-accent)] last:border-0"
                  >
                    <td className="py-2 pr-4 font-mono text-xs">{ep.pathPattern}</td>
                    <td className="py-2 pr-4">{t("millisecondsValue", { value: ep.p95Ms.toFixed(1) })}</td>
                    <td className="py-2 pr-4">{t("millisecondsValue", { value: ep.p99Ms.toFixed(1) })}</td>
                    <td className="py-2">{(ep.errorRate * 100).toFixed(1)}%</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="rounded-lg border border-indigo-800 bg-black/20 p-4">
        <h2 className="mb-3 text-lg font-semibold text-[var(--color-platform-on-accent)]">
          {t("endpointsTitle")}
        </h2>
        {summary.endpoints.length === 0 ? (
          <p className="text-sm text-indigo-200">{t("empty")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-indigo-800 text-indigo-200">
                  <th className="py-2 pr-4 font-medium">{t("columnEndpoint")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnP50")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnP95")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnP99")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnErrorRate")}</th>
                  <th className="py-2 font-medium">{t("columnRequestCount")}</th>
                </tr>
              </thead>
              <tbody>
                {summary.endpoints.map((ep) => (
                  <tr
                    key={ep.pathPattern}
                    className="border-b border-indigo-900 text-[var(--color-platform-on-accent)] last:border-0"
                  >
                    <td className="py-2 pr-4 font-mono text-xs">{ep.pathPattern}</td>
                    <td className="py-2 pr-4">{t("millisecondsValue", { value: ep.p50Ms.toFixed(1) })}</td>
                    <td className="py-2 pr-4">{t("millisecondsValue", { value: ep.p95Ms.toFixed(1) })}</td>
                    <td className="py-2 pr-4">{t("millisecondsValue", { value: ep.p99Ms.toFixed(1) })}</td>
                    <td className="py-2 pr-4">{(ep.errorRate * 100).toFixed(1)}%</td>
                    <td className="py-2">{ep.requestCount}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
