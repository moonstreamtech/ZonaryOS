// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package apidocs is the plugin/extension architecture + developer
// API/SDK foundation batch's own Part 4: GET /api/docs serves this
// installation's OpenAPI 3.0 spec (docs/api/openapi.yaml, the same file
// scripts/check_api_contract.py already keeps in sync with the real
// route surface on every PR) as raw YAML - no UI rendering, no
// authentication, so an external tool (Swagger UI, Postman, an SDK
// generator) can import it directly by URL.
package apidocs

import (
	"net/http"

	"github.com/moonstreamtech/ZonaryOS/docs/api"
)

// contentTypeYAML is the informal but widely-recognized YAML MIME type -
// OpenAPI tooling (Swagger UI, Postman, openapi-generator) accepts either
// this or a plain text/plain; served explicitly rather than left to
// Go's own content-sniffing (which has no notion of YAML at all and
// would otherwise guess text/plain, which still works but is less
// precise about what's actually being served).
const contentTypeYAML = "application/yaml"

// RegisterRoutes wires GET /api/docs into mux - deliberately
// unauthenticated (it's documentation, per this batch's own brief) and
// registered directly on mux (not through identity.Middleware the way
// every firm-scoped route group in this codebase is), the same
// unauthenticated-GET posture internal/discovery.RegisterRoutes' own
// well-known endpoint and internal/currency's GET /api/exchange-rates
// already establish for non-sensitive, read-only content.
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/docs", handleServeSpec)
}

func handleServeSpec(w http.ResponseWriter, r *http.Request) {
	spec, err := openapi.FS.ReadFile("openapi.yaml")
	if err != nil {
		http.Error(w, "openapi spec unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentTypeYAML)
	_, _ = w.Write(spec)
}
