// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package alerting

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

const recentEventsLimit = 50

// RegisterRoutes wires this package's CRUD + recent-events HTTP surface
// into mux - platform-admin-only throughout, same allowlist-then-404
// convention as every other platform-admin route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, allow platformadmin.Allowlist) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/platform-admin/alert-rules", auth(http.HandlerFunc(handleListRules(pool, allow))))
	mux.Handle("POST /api/platform-admin/alert-rules", auth(http.HandlerFunc(handleCreateRule(pool, allow))))
	mux.Handle("PATCH /api/platform-admin/alert-rules/{ruleID}", auth(http.HandlerFunc(handleUpdateRule(pool, allow))))
	mux.Handle("DELETE /api/platform-admin/alert-rules/{ruleID}", auth(http.HandlerFunc(handleDeleteRule(pool, allow))))
	mux.Handle("GET /api/platform-admin/alerts", auth(http.HandlerFunc(handleListEvents(pool, allow))))
}

func checkAllow(w http.ResponseWriter, r *http.Request, allow platformadmin.Allowlist) bool {
	id, ok := identity.FromContext(r.Context())
	if !ok {
		http.Error(w, "missing identity", http.StatusInternalServerError)
		return false
	}
	if !allow.Contains(id.Email) {
		http.NotFound(w, r)
		return false
	}
	return true
}

func writeAlertingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRule):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrRuleNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type ruleRequest struct {
	Name          string  `json:"name"`
	Metric        string  `json:"metric"`
	Threshold     float64 `json:"threshold"`
	WindowMinutes int     `json:"windowMinutes"`
	IsActive      bool    `json:"isActive"`
}

func (req ruleRequest) toInput() RuleInput {
	return RuleInput{Name: req.Name, Metric: Metric(req.Metric), Threshold: req.Threshold, WindowMinutes: req.WindowMinutes, IsActive: req.IsActive}
}

type ruleResponse struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Metric        string  `json:"metric"`
	Threshold     float64 `json:"threshold"`
	WindowMinutes int     `json:"windowMinutes"`
	IsActive      bool    `json:"isActive"`
	CreatedAt     string  `json:"createdAt"`
}

func toRuleResponse(r AlertRule) ruleResponse {
	return ruleResponse{
		ID: r.ID.String(), Name: r.Name, Metric: string(r.Metric), Threshold: r.Threshold,
		WindowMinutes: r.WindowMinutes, IsActive: r.IsActive, CreatedAt: r.CreatedAt.Format(time.RFC3339),
	}
}

func handleListRules(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkAllow(w, r, allow) {
			return
		}
		rules, err := ListRules(r.Context(), pool)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		resp := make([]ruleResponse, 0, len(rules))
		for _, rule := range rules {
			resp = append(resp, toRuleResponse(rule))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleCreateRule(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkAllow(w, r, allow) {
			return
		}
		var req ruleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		rule, err := CreateRule(r.Context(), pool, req.toInput())
		if err != nil {
			writeAlertingError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toRuleResponse(rule))
	}
}

func handleUpdateRule(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkAllow(w, r, allow) {
			return
		}
		ruleID, err := uuid.Parse(r.PathValue("ruleID"))
		if err != nil {
			http.Error(w, "invalid ruleID", http.StatusBadRequest)
			return
		}
		var req ruleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		rule, err := UpdateRule(r.Context(), pool, ruleID, req.toInput())
		if err != nil {
			writeAlertingError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toRuleResponse(rule))
	}
}

func handleDeleteRule(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkAllow(w, r, allow) {
			return
		}
		ruleID, err := uuid.Parse(r.PathValue("ruleID"))
		if err != nil {
			http.Error(w, "invalid ruleID", http.StatusBadRequest)
			return
		}
		if err := DeleteRule(r.Context(), pool, ruleID); err != nil {
			writeAlertingError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type eventResponse struct {
	ID          string  `json:"id"`
	RuleID      string  `json:"ruleId"`
	RuleName    string  `json:"ruleName"`
	TriggeredAt string  `json:"triggeredAt"`
	MetricValue float64 `json:"metricValue"`
	ResolvedAt  *string `json:"resolvedAt,omitempty"`
}

func handleListEvents(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkAllow(w, r, allow) {
			return
		}
		events, err := ListRecentEvents(r.Context(), pool, recentEventsLimit)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		resp := make([]eventResponse, 0, len(events))
		for _, e := range events {
			er := eventResponse{
				ID: e.ID.String(), RuleID: e.RuleID.String(), RuleName: e.RuleName,
				TriggeredAt: e.TriggeredAt.Format(time.RFC3339), MetricValue: e.MetricValue,
			}
			if e.ResolvedAt != nil {
				s := e.ResolvedAt.Format(time.RFC3339)
				er.ResolvedAt = &s
			}
			resp = append(resp, er)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
