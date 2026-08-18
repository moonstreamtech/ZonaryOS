// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package httpapi assembles the HTTP router shared by all ZonaryOS modules.
//
// Each domain module (identity, firm, inventory, sales, ...) registers its
// own routes against the mux returned by NewMux; this package owns only the
// cross-cutting concerns (routing, health checks, and — in later slices —
// the auth and permission middleware chain).
package httpapi

import (
	"encoding/json"
	"net/http"
)

// NewMux builds the root HTTP handler for the ZonaryOS server binary.
func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /api/version", handleVersion)
	return mux
}

type healthResponse struct {
	Status string `json:"status"`
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}

// currentAPIVersion/supportedAPIVersions (mobile API optimization batch,
// Part 4) are this package's own single source of truth for GET
// /api/version - see docs/DEVELOPMENT.md's "API versioning policy"
// section for what bumping either actually means in practice. Today
// there is exactly one real version (every /api/... route, aliased at
// /api/v1/... by internal/platform/middleware.APIVersionAlias) - this
// list grows only when a genuinely breaking change ships a real v2 route
// set, not on every additive change.
const currentAPIVersion = "v1"

var supportedAPIVersions = []string{"v1"}

type versionResponse struct {
	Version           string   `json:"version"`
	SupportedVersions []string `json:"supportedVersions"`
}

// handleVersion is unauthenticated, unconditional - a client (mobile or
// otherwise) needs to be able to check API version compatibility before
// it has any session/token at all, the same reasoning GET /healthz
// already has no auth gate.
func handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(versionResponse{Version: currentAPIVersion, SupportedVersions: supportedAPIVersions})
}
