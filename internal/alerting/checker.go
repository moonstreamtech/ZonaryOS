// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package alerting

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/apm"
)

// DefaultCheckerPollInterval is Part 2's own "evaluates active rules
// every 5 minutes" requirement.
const DefaultCheckerPollInterval = 5 * time.Minute

// RunChecker runs ProcessAlertChecks every pollInterval until ctx is
// cancelled - the same RunX/ProcessX split every other background
// scheduler in this codebase already uses.
func RunChecker(ctx context.Context, pool *pgxpool.Pool, adminEmails []string, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ProcessAlertChecks(ctx, pool, adminEmails)
		}
	}
}

// ProcessAlertChecks is RunChecker's testable core: every active
// alert_rules row is evaluated against its own metric/window_minutes;
// a breach (see evaluateMetric/isBreached) creates one alert_events row
// and notifies every platform admin email. This batch does not
// deduplicate repeated breaches across consecutive polls - a metric
// that stays over threshold for e.g. 30 minutes with a 5-minute poll
// interval creates 6 separate alert_events rows, one per poll, rather
// than one open incident that gets updated - the simplest correct
// behavior for this batch's own scope (an "open incident" model needing
// alert_events.resolved_at to be set automatically once a metric
// recovers is real future work, not solved here; resolved_at exists in
// the schema today only for a future manual acknowledge/resolve
// endpoint - see migrations/0041's own comment).
func ProcessAlertChecks(ctx context.Context, pool *pgxpool.Pool, adminEmails []string) {
	rules, err := ActiveRules(ctx, pool)
	if err != nil {
		slog.Error("alerting: list active rules", "err", err)
		return
	}
	for _, rule := range rules {
		checkOneRule(ctx, pool, rule, adminEmails)
	}
}

func checkOneRule(ctx context.Context, pool *pgxpool.Pool, rule AlertRule, adminEmails []string) {
	value, err := evaluateMetric(ctx, pool, rule)
	if err != nil {
		slog.Error("alerting: evaluate metric", "ruleId", rule.ID, "metric", rule.Metric, "err", err)
		return
	}
	if !isBreached(value, rule.Threshold) {
		return
	}

	event, err := recordAlertEvent(ctx, pool, rule.ID, value)
	if err != nil {
		slog.Error("alerting: record alert event", "ruleId", rule.ID, "err", err)
		return
	}
	notifyPlatformAdmins(rule, event, adminEmails)
}

// isBreached is a strict greater-than: a rule with threshold=0 (e.g.
// error_rate) triggers the instant the metric is above zero at all -
// "any error" - rather than requiring it to exceed zero by some margin
// a >= comparison with a zero threshold could never actually satisfy
// meaningfully differently from > anyway, but > is the correct, honest
// reading of "threshold breached" for every one of this batch's three
// metrics (error_rate/response_time_p95/volume_drop are all "the metric
// got worse than this ceiling", never "at or below").
func isBreached(value, threshold float64) bool {
	return value > threshold
}

// evaluateMetric computes rule's own metric value over its own
// window_minutes, using internal/apm's shared aggregate-query helpers
// (see that package's own doc comment for why the SQL lives there, not
// here).
func evaluateMetric(ctx context.Context, pool *pgxpool.Pool, rule AlertRule) (float64, error) {
	switch rule.Metric {
	case MetricErrorRate:
		return apm.ErrorRate(ctx, pool, rule.WindowMinutes)
	case MetricResponseTimeP95:
		return apm.ResponseTimeP95(ctx, pool, rule.WindowMinutes)
	case MetricVolumeDrop:
		return volumeDropRatio(ctx, pool, rule.WindowMinutes)
	default:
		return 0, fmt.Errorf("unrecognized metric %q", rule.Metric)
	}
}

// volumeDropRatio compares the current window's own request volume
// against the equivalent PRIOR window (immediately before it, same
// length) - e.g. with window_minutes=60, "this past hour" vs "the hour
// before that" - and returns the fractional drop (0 = no drop or an
// increase, 1 = traffic went to zero). A previous window with zero
// requests of its own is treated as "no drop to measure" (0), not a
// divide-by-zero or a misleadingly enormous ratio - there is nothing
// meaningful to compare a real drop against when the baseline itself
// was empty.
func volumeDropRatio(ctx context.Context, pool *pgxpool.Pool, windowMinutes int) (float64, error) {
	now := time.Now().UTC()
	windowDur := time.Duration(windowMinutes) * time.Minute
	currentStart := now.Add(-windowDur)
	previousStart := currentStart.Add(-windowDur)

	current, err := apm.RequestVolumeBetween(ctx, pool, currentStart, now)
	if err != nil {
		return 0, err
	}
	previous, err := apm.RequestVolumeBetween(ctx, pool, previousStart, currentStart)
	if err != nil {
		return 0, err
	}
	if previous == 0 {
		return 0, nil
	}
	if current >= previous {
		return 0, nil
	}
	return float64(previous-current) / float64(previous), nil
}

func recordAlertEvent(ctx context.Context, pool *pgxpool.Pool, ruleID uuid.UUID, metricValue float64) (AlertEvent, error) {
	var e AlertEvent
	err := pool.QueryRow(ctx, `
		INSERT INTO alert_events (rule_id, metric_value)
		VALUES ($1, $2)
		RETURNING id, rule_id, triggered_at, metric_value::float8, resolved_at
	`, ruleID, metricValue).Scan(&e.ID, &e.RuleID, &e.TriggeredAt, &e.MetricValue, &e.ResolvedAt)
	if err != nil {
		return AlertEvent{}, fmt.Errorf("insert alert event: %w", err)
	}
	return e, nil
}

// notifyPlatformAdmins is this batch's own "notification" channel for
// platform admins: a structured slog.Warn line, addressed to each
// configured platform-admin email individually, one per breach. There
// is no real email/push delivery in this codebase at all yet (Open
// Points item 17, deliberately deferred) - the same limitation
// internal/scheduledreports' own in-app notifications already accept
// for firm-scoped recipients. Platform admins are identified only by
// email (internal/platformadmin.Allowlist), with no corresponding
// firm-scoped ZonaryOS user row to address an internal/notification
// row to (that table is firm-scoped by design - see its own doc
// comment) - so a structured log line an operator's own log
// aggregation can alert on is the honest, consistent channel for this
// batch, not a new and separate email-sending mechanism this batch
// would otherwise have to build from scratch.
func notifyPlatformAdmins(rule AlertRule, event AlertEvent, adminEmails []string) {
	for _, email := range adminEmails {
		slog.Warn("alerting: threshold breached, notifying platform admin",
			"ruleId", rule.ID, "ruleName", rule.Name, "metric", rule.Metric,
			"threshold", rule.Threshold, "metricValue", event.MetricValue,
			"alertEventId", event.ID, "platformAdminEmail", email)
	}
}
