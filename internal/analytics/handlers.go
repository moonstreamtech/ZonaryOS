// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package analytics

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func resolveIdentity(r *http.Request, pool *pgxpool.Pool) (firmID, userID uuid.UUID, ok bool, status int, msg string) {
	id, present := identity.FromContext(r.Context())
	if !present {
		return uuid.UUID{}, uuid.UUID{}, false, http.StatusInternalServerError, "missing identity"
	}
	firmID, err := uuid.Parse(r.PathValue("firmID"))
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, false, http.StatusBadRequest, "invalid firm id"
	}
	userID, err = identity.ResolveOrCreateUser(r.Context(), pool, id)
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, false, http.StatusInternalServerError, "failed to resolve user"
	}
	return firmID, userID, true, 0, ""
}

func writeAnalyticsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrInvalidEventBatch), errors.Is(err, ErrInvalidQuery):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// RegisterRoutes wires this package's HTTP surface - all member-gated
// (see IngestEvents/TimeSeriesQuery/TopByProperty/ActiveUserCount's own
// doc comments for why: analytics is ordinary firm data visibility, not
// a structural firm decision).
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)
	mux.Handle("POST /api/firms/{firmID}/analytics/events", auth(http.HandlerFunc(handleIngestEvents(pool))))
	mux.Handle("POST /api/firms/{firmID}/analytics/query", auth(http.HandlerFunc(handleTimeSeriesQuery(pool))))
	mux.Handle("GET /api/firms/{firmID}/analytics/top", auth(http.HandlerFunc(handleTopByProperty(pool))))
	mux.Handle("GET /api/firms/{firmID}/analytics/active-users", auth(http.HandlerFunc(handleActiveUserCount(pool))))
}

type eventRequest struct {
	EventType  string         `json:"eventType"`
	EntityType string         `json:"entityType,omitempty"`
	EntityID   *string        `json:"entityId,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	OccurredAt *string        `json:"occurredAt,omitempty"`
}

func (r eventRequest) toInput() (EventInput, error) {
	input := EventInput{EventType: r.EventType, EntityType: r.EntityType, Properties: r.Properties}
	if r.EntityID != nil && *r.EntityID != "" {
		id, err := uuid.Parse(*r.EntityID)
		if err != nil {
			return EventInput{}, err
		}
		input.EntityID = &id
	}
	if r.OccurredAt != nil && *r.OccurredAt != "" {
		t, err := time.Parse(time.RFC3339, *r.OccurredAt)
		if err != nil {
			return EventInput{}, err
		}
		input.OccurredAt = &t
	}
	return input, nil
}

// handleIngestEvents accepts POST .../analytics/events with a JSON array
// body of events (max 100, see maxEventBatchSize) - Part 1's own
// server-side ingestion point.
func handleIngestEvents(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		var reqs []eventRequest
		if err := decodeJSONBody(r, &reqs); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		events := make([]EventInput, 0, len(reqs))
		for _, req := range reqs {
			input, err := req.toInput()
			if err != nil {
				http.Error(w, "invalid event: "+err.Error(), http.StatusBadRequest)
				return
			}
			events = append(events, input)
		}

		if err := IngestEvents(r.Context(), pool, firmID, userID, events); err != nil {
			writeAnalyticsError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

type propertyFilterRequest struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

type timeSeriesQueryRequest struct {
	EventType string                  `json:"event_type"`
	GroupBy   string                  `json:"group_by"`
	From      string                  `json:"from"`
	To        string                  `json:"to"`
	Filters   []propertyFilterRequest `json:"filters,omitempty"`
}

// parseQueryDate accepts either a plain YYYY-MM-DD date (this batch's
// own design-brief example, "from": "2026-01-01") or a full RFC3339
// timestamp - the same "accept the friendlier common case, still accept
// the fully-precise one" flexibility internal/integration's own
// formatDate transform already establishes for the identical ambiguity.
func parseQueryDate(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", raw)
}

type timeSeriesBucketResponse struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

// handleTimeSeriesQuery accepts POST .../analytics/query, Part 2's own
// time-series query spec.
func handleTimeSeriesQuery(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		var req timeSeriesQueryRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		from, err := parseQueryDate(req.From)
		if err != nil {
			http.Error(w, "invalid \"from\" date", http.StatusBadRequest)
			return
		}
		to, err := parseQueryDate(req.To)
		if err != nil {
			http.Error(w, "invalid \"to\" date", http.StatusBadRequest)
			return
		}
		filters := make([]PropertyFilter, 0, len(req.Filters))
		for _, f := range req.Filters {
			filters = append(filters, PropertyFilter{Property: f.Property, Value: f.Value})
		}

		buckets, err := TimeSeriesQuery(r.Context(), pool, firmID, userID, TimeSeriesQuerySpec{
			EventType: req.EventType, GroupBy: TimeSeriesGroupBy(req.GroupBy), From: from, To: to, Filters: filters,
		})
		if err != nil {
			writeAnalyticsError(w, err)
			return
		}
		resp := make([]timeSeriesBucketResponse, 0, len(buckets))
		for _, b := range buckets {
			resp = append(resp, timeSeriesBucketResponse{Bucket: b.Bucket.Format(time.RFC3339), Count: b.Count})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type propertyCountResponse struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// handleTopByProperty accepts GET .../analytics/top?eventType=...&property=...&limit=...
// - the shared endpoint behind both "top workflows by execution count"
// and the page-view heatmap table (see TopByProperty's own doc comment).
func handleTopByProperty(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				limit = n
			}
		}

		counts, err := TopByProperty(r.Context(), pool, firmID, userID, r.URL.Query().Get("eventType"), r.URL.Query().Get("property"), limit)
		if err != nil {
			writeAnalyticsError(w, err)
			return
		}
		resp := make([]propertyCountResponse, 0, len(counts))
		for _, c := range counts {
			resp = append(resp, propertyCountResponse{Value: c.Value, Count: c.Count})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type activeUserCountResponse struct {
	Count int `json:"count"`
	Days  int `json:"days"`
}

// handleActiveUserCount accepts GET .../analytics/active-users?days=30.
func handleActiveUserCount(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		days := 30
		if raw := r.URL.Query().Get("days"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				days = n
			}
		}

		count, err := ActiveUserCount(r.Context(), pool, firmID, userID, days)
		if err != nil {
			writeAnalyticsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(activeUserCountResponse{Count: count, Days: days})
	}
}
