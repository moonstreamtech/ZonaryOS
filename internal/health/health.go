// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package health implements GET /health, the observability foundation's
// operator-facing "is this installation actually working" endpoint -
// distinct from internal/platform/httpapi's existing GET /healthz, which
// only ever reports the process itself is up and answering HTTP (a
// liveness probe, not a dependency check). GET /health additionally
// checks the two external dependencies every ZonaryOS request path
// ultimately relies on: Postgres (via a lightweight ping) and Keycloak
// (via its own OIDC discovery document) - see Check's doc comment for
// why both are checked in parallel rather than sequentially, and
// docs/DEVELOPMENT.md for the CI Checklist entry this satisfies.
//
// Unauthenticated by design: an operator (or an external uptime monitor)
// needs to be able to check this before any credential exists to present,
// the same reasoning GET /healthz and GET /.well-known/zonaryos-central
// already rest on. It reports only aggregate up/down status per
// dependency - no connection strings, no error detail, no stack traces -
// so it carries no more information-disclosure risk than any other
// unauthenticated liveness endpoint.
//
// Part 3 of the performance monitoring/alerting/operational excellence
// batch extends this same endpoint (rather than adding a new one) with
// a `database_detail` block - active connection count, oldest running
// query, top-5 table bloat estimate, and request_metrics partition
// health - but ONLY when `?detail=true` is passed, so the plain,
// unauthenticated GET /health every uptime monitor already polls stays
// exactly as fast/cheap as before this batch. See buildDatabaseDetail.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/apm"
	"github.com/moonstreamtech/ZonaryOS/internal/pghealth"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/version"
)

// checkTimeout bounds how long either individual check may take, so a
// wedged dependency degrades this endpoint's own response time by a
// fixed, small amount rather than hanging indefinitely.
const checkTimeout = 5 * time.Second

// CheckStatus is one dependency's own up/down verdict.
type CheckStatus string

const (
	StatusOK    CheckStatus = "ok"
	StatusError CheckStatus = "error"
)

// Status is the overall installation-health verdict GET /health reports:
// StatusOK only if every check passed, StatusError only if every check
// failed, StatusDegraded for anything in between - the same three-tier
// shape docs/DEVELOPMENT.md's CI Checklist section documents for this
// endpoint.
type OverallStatus string

const (
	OverallOK       OverallStatus = "ok"
	OverallDegraded OverallStatus = "degraded"
	OverallError    OverallStatus = "error"
)

type response struct {
	Status         OverallStatus          `json:"status"`
	Checks         map[string]CheckStatus `json:"checks"`
	Version        string                 `json:"version"`
	DatabaseDetail *databaseDetail        `json:"database_detail,omitempty"`
}

// databaseDetail is Part 3's own "GET /health?detail=true" payload -
// read-only Postgres system-view queries (internal/pghealth), never
// returned on a plain GET /health so the fast, unauthenticated liveness
// check every uptime monitor hits stays cheap - these extra queries only
// run when a caller explicitly opts in via the query string.
type databaseDetail struct {
	ActiveConnections      int              `json:"activeConnections"`
	OldestRunningQuerySecs *float64         `json:"oldestRunningQuerySeconds"`
	TableBloat             []tableBloatItem `json:"tableBloatTop5"`
	PartitionHealth        partitionHealth  `json:"requestMetricsPartitionHealth"`
}

type tableBloatItem struct {
	TableName string  `json:"tableName"`
	DeadRatio float64 `json:"deadRatio"`
	TotalSize string  `json:"totalSize"`
}

type partitionHealth struct {
	Healthy       bool     `json:"healthy"`
	MissingMonths []string `json:"missingMonths,omitempty"`
}

const tableBloatTop = 5

// buildDatabaseDetail runs Part 3's own four read-only checks. Errors
// are logged nowhere here (deliberately) and simply degrade that one
// field to its zero value - a caller who explicitly opted into
// ?detail=true is an operator debugging a live problem, and a partial,
// best-effort detail payload is more useful to them than a 500 that
// throws away the checks that DID succeed.
func buildDatabaseDetail(ctx context.Context, pool *pgxpool.Pool) *databaseDetail {
	if pool == nil {
		return nil
	}
	detail := &databaseDetail{}

	if n, err := pghealth.ActiveConnectionCount(ctx, pool); err == nil {
		detail.ActiveConnections = n
	}
	if secs, err := pghealth.OldestRunningQuerySeconds(ctx, pool); err == nil {
		detail.OldestRunningQuerySecs = secs
	}
	if bloat, err := pghealth.TableBloatEstimate(ctx, pool, tableBloatTop); err == nil {
		for _, b := range bloat {
			detail.TableBloat = append(detail.TableBloat, tableBloatItem{TableName: b.TableName, DeadRatio: b.DeadRatio, TotalSize: b.TotalSizeStr})
		}
	}
	if ph, err := apm.CheckPartitionHealth(ctx, pool); err == nil {
		detail.PartitionHealth = partitionHealth{Healthy: len(ph.MissingMonths) == 0, MissingMonths: ph.MissingMonths}
	}
	return detail
}

// OIDCDiscoveryURL builds the well-known discovery document URL Handler
// probes to confirm Keycloak is reachable - the same document
// internal/identity.NewVerifier itself fetches at startup via
// go-oidc's oidc.NewProvider, just re-probed here on every /health
// request rather than once at boot.
func OIDCDiscoveryURL(issuerURL string) string {
	return issuerURL + "/.well-known/openid-configuration"
}

// Handler returns the GET /health HTTP handler. pool is pinged directly
// (a cheap, connection-level round trip - not a full query); httpClient
// issues a GET against discoveryURL and only checks for a 2xx response,
// the same "is it reachable and answering" bar the database check uses,
// not a schema-level validation of the OIDC document's contents.
//
// The two checks run concurrently via sync.WaitGroup rather than one
// after another: with a checkTimeout ceiling on each, a caller
// (transitively) waiting on both would otherwise see up to 2x
// checkTimeout in the worst case (one dependency wedged, checked first)
// instead of the ~1x checkTimeout this endpoint actually promises by
// running them in parallel.
func Handler(pool *pgxpool.Pool, discoveryURL string, httpClient *http.Client) http.HandlerFunc {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return func(w http.ResponseWriter, r *http.Request) {
		checks := runChecks(r.Context(), pool, discoveryURL, httpClient)

		overall := OverallOK
		okCount := 0
		for _, s := range checks {
			if s == StatusOK {
				okCount++
			}
		}
		switch {
		case okCount == len(checks):
			overall = OverallOK
		case okCount == 0:
			overall = OverallError
		default:
			overall = OverallDegraded
		}

		resp := response{Status: overall, Checks: checks, Version: version.Version}
		if r.URL.Query().Get("detail") == "true" {
			resp.DatabaseDetail = buildDatabaseDetail(r.Context(), pool)
		}

		w.Header().Set("Content-Type", "application/json")
		if overall != OverallOK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func runChecks(ctx context.Context, pool *pgxpool.Pool, discoveryURL string, httpClient *http.Client) map[string]CheckStatus {
	checks := map[string]CheckStatus{"database": StatusError, "keycloak": StatusError}
	var mu sync.Mutex
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		status := checkDatabase(ctx, pool)
		mu.Lock()
		checks["database"] = status
		mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		status := checkKeycloak(ctx, discoveryURL, httpClient)
		mu.Lock()
		checks["keycloak"] = status
		mu.Unlock()
	}()

	wg.Wait()
	return checks
}

func checkDatabase(ctx context.Context, pool *pgxpool.Pool) CheckStatus {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	if pool == nil {
		return StatusError
	}
	if err := pool.Ping(ctx); err != nil {
		return StatusError
	}
	return StatusOK
}

func checkKeycloak(ctx context.Context, discoveryURL string, httpClient *http.Client) CheckStatus {
	if discoveryURL == "" {
		return StatusError
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return StatusError
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return StatusError
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return StatusError
	}
	return StatusOK
}
