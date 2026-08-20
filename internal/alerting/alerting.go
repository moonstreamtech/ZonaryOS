// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package alerting is Part 2 of the performance monitoring, alerting,
// and operational excellence batch: platform-level alert rules
// (alert_rules/alert_events, migrations/0041 - see that migration's own
// doc comment for why neither table needs RLS) evaluated on a background
// schedule against internal/apm's own aggregate-query helpers
// (ErrorRate/ResponseTimeP95/RequestVolume) rather than duplicating that
// SQL here - the same "one primitive, two callers" reasoning
// internal/apm's own doc comment already explains.
//
// Deliberately platform-admin-gated, not firm-scoped: an alert rule like
// "error rate above 5% in the last hour" is a statement about the whole
// installation, so both CRUD and the recent-events read use the same
// allowlist-then-404 convention internal/platformadmin/internal/currency
// already establish, never a firm membership check.
package alerting

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Metric is alert_rules.metric's own small, open vocabulary - Checker's
// entire switch (see checker.go) recognizes exactly these three today;
// an unrecognized value is rejected at write time by validateMetric, not
// silently accepted and then never evaluated.
type Metric string

const (
	MetricErrorRate       Metric = "error_rate"
	MetricResponseTimeP95 Metric = "response_time_p95"
	MetricVolumeDrop      Metric = "volume_drop"
)

func validMetric(m Metric) bool {
	switch m {
	case MetricErrorRate, MetricResponseTimeP95, MetricVolumeDrop:
		return true
	default:
		return false
	}
}

// AlertRule is one alert_rules row.
type AlertRule struct {
	ID            uuid.UUID
	Name          string
	Metric        Metric
	Threshold     float64
	WindowMinutes int
	IsActive      bool
	CreatedAt     time.Time
}

// AlertEvent is one alert_events row - one recorded threshold breach.
type AlertEvent struct {
	ID          uuid.UUID
	RuleID      uuid.UUID
	TriggeredAt time.Time
	MetricValue float64
	ResolvedAt  *time.Time
}

// RuleInput is CreateRule/UpdateRule's request shape.
type RuleInput struct {
	Name          string
	Metric        Metric
	Threshold     float64
	WindowMinutes int
	IsActive      bool
}

func validateRuleInput(input RuleInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidRule)
	}
	if !validMetric(input.Metric) {
		return fmt.Errorf("%w: unrecognized metric %q", ErrInvalidRule, input.Metric)
	}
	if input.WindowMinutes <= 0 {
		return fmt.Errorf("%w: windowMinutes must be positive", ErrInvalidRule)
	}
	return nil
}

const alertRuleColumns = "id, name, metric, threshold::float8, window_minutes, is_active, created_at"

func scanAlertRule(row pgx.Row) (AlertRule, error) {
	var r AlertRule
	var metric string
	if err := row.Scan(&r.ID, &r.Name, &metric, &r.Threshold, &r.WindowMinutes, &r.IsActive, &r.CreatedAt); err != nil {
		return AlertRule{}, err
	}
	r.Metric = Metric(metric)
	return r, nil
}

// CreateRule inserts a new alert_rules row - platform-admin-gated at the
// HTTP layer (handlers.go), no authorization check of its own (this is
// a plain platform-level write primitive, not itself the enforcement
// point).
func CreateRule(ctx context.Context, pool *pgxpool.Pool, input RuleInput) (AlertRule, error) {
	if err := validateRuleInput(input); err != nil {
		return AlertRule{}, err
	}
	row := pool.QueryRow(ctx, `
		INSERT INTO alert_rules (name, metric, threshold, window_minutes, is_active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+alertRuleColumns, input.Name, string(input.Metric), input.Threshold, input.WindowMinutes, input.IsActive)
	return scanAlertRule(row)
}

// ListRules returns every alert_rules row, most-recently-created first.
func ListRules(ctx context.Context, pool *pgxpool.Pool) ([]AlertRule, error) {
	rows, err := pool.Query(ctx, `SELECT `+alertRuleColumns+` FROM alert_rules ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}
	defer rows.Close()

	var rules []AlertRule
	for rows.Next() {
		r, err := scanAlertRule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alert rule row: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// ActiveRules returns only is_active rules - Checker's own input set
// (checker.go).
func ActiveRules(ctx context.Context, pool *pgxpool.Pool) ([]AlertRule, error) {
	rows, err := pool.Query(ctx, `SELECT `+alertRuleColumns+` FROM alert_rules WHERE is_active ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list active alert rules: %w", err)
	}
	defer rows.Close()

	var rules []AlertRule
	for rows.Next() {
		r, err := scanAlertRule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan active alert rule row: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// UpdateRule replaces ruleID's own name/metric/threshold/windowMinutes/
// isActive in one statement (this batch's own CRUD needs nothing more
// granular - a partial-field PATCH isn't required by Part 2's own
// brief).
func UpdateRule(ctx context.Context, pool *pgxpool.Pool, ruleID uuid.UUID, input RuleInput) (AlertRule, error) {
	if err := validateRuleInput(input); err != nil {
		return AlertRule{}, err
	}
	row := pool.QueryRow(ctx, `
		UPDATE alert_rules SET name = $1, metric = $2, threshold = $3, window_minutes = $4, is_active = $5
		WHERE id = $6
		RETURNING `+alertRuleColumns, input.Name, string(input.Metric), input.Threshold, input.WindowMinutes, input.IsActive, ruleID)
	rule, err := scanAlertRule(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return AlertRule{}, ErrRuleNotFound
		}
		return AlertRule{}, err
	}
	return rule, nil
}

// DeleteRule removes ruleID - ON DELETE CASCADE (migrations/0041) also
// removes its own past alert_events rows, the same "a deleted parent's
// history goes with it" posture this codebase already applies
// elsewhere (e.g. scheduled_reports -> its own saved runs).
func DeleteRule(ctx context.Context, pool *pgxpool.Pool, ruleID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM alert_rules WHERE id = $1`, ruleID)
	if err != nil {
		return fmt.Errorf("delete alert rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRuleNotFound
	}
	return nil
}

// ListRecentEvents returns the most recent alert_events (joined with
// their own rule's name, so a caller never needs a second round trip
// just to label an event), most-recent-first, capped at limit.
type EventWithRuleName struct {
	AlertEvent
	RuleName string
}

func ListRecentEvents(ctx context.Context, pool *pgxpool.Pool, limit int) ([]EventWithRuleName, error) {
	rows, err := pool.Query(ctx, `
		SELECT e.id, e.rule_id, e.triggered_at, e.metric_value::float8, e.resolved_at, r.name
		FROM alert_events e
		JOIN alert_rules r ON r.id = e.rule_id
		ORDER BY e.triggered_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent alert events: %w", err)
	}
	defer rows.Close()

	var events []EventWithRuleName
	for rows.Next() {
		var e EventWithRuleName
		if err := rows.Scan(&e.ID, &e.RuleID, &e.TriggeredAt, &e.MetricValue, &e.ResolvedAt, &e.RuleName); err != nil {
			return nil, fmt.Errorf("scan alert event row: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
