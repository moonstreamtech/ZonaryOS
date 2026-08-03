// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package discovery is the shared "where is the central server right now"
// mechanism every installation's backend uses (Open Points item 34: there
// is only one real ZonaryOS instance today, zonaryos.duckdns.org - no
// separate central server exists yet). It exists so a future real
// central-server migration never requires a code change or a manual
// config update on every already-deployed installation.
//
// # The two halves
//
// 1. The SERVER half (this file, HandleWellKnown/RegisterRoutes): every
// installation serves GET /.well-known/zonaryos-central, a small JSON
// document naming the current real central-server URL. Today that's
// self-referential - every installation answers with its own configured
// public URL (ZONARYOS_PUBLIC_URL, the same "what is my own public
// address" convention docker-compose.prod.yml's KC_HOSTNAME already
// establishes for Keycloak's own external identity - see that file's
// comment) - because there genuinely is no separate central server yet.
// The document's shape is designed to survive that changing: once a real
// central server exists, ITS installation serves this same endpoint,
// pointing at itself, and every other installation's discovery client
// (below) eventually picks that up without any code or config change.
//
// 2. The CLIENT half (client.go, Client/Resolve): what
// internal/license (whenever it grows a real network-issuance path) and
// internal/telemetry (which needs one today) call to answer "where do I
// send/check things centrally, right now." See Client's doc comment for
// the cache/TTL/fallback design.
package discovery

import (
	"encoding/json"
	"net/http"
)

// WellKnownDocument is the JSON shape served at
// GET /.well-known/zonaryos-central and expected back by Client.Resolve.
type WellKnownDocument struct {
	// CentralURL is the current real central-server base URL (no trailing
	// slash), as this installation currently understands it. Today every
	// installation answers with its own ZONARYOS_PUBLIC_URL - see this
	// package's doc comment for why that's an honest, forward-compatible
	// starting point rather than a placeholder to be embarrassed about.
	CentralURL string `json:"centralUrl"`
}

// HandleWellKnown returns the GET /.well-known/zonaryos-central handler.
// publicURL is this installation's own configured public base URL
// (ZONARYOS_PUBLIC_URL - see internal/platform/config); an installation
// that hasn't configured one yet (e.g. plain local dev, which has no
// public URL at all) still serves the endpoint, just with an empty
// centralUrl - a discovery client treats that the same as "unreachable"
// (see Client.Resolve), never as a value to actually adopt.
func HandleWellKnown(publicURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(WellKnownDocument{CentralURL: publicURL})
	}
}

// RegisterRoutes wires the well-known discovery endpoint into mux.
// Unconditional and unauthenticated - unlike internal/license and
// internal/telemetry, this endpoint has no default-off gate: it names
// this installation's own already-public URL (the same value a browser
// hitting the site already sees), so there is no new information
// disclosed by serving it, and every future consumer (this installation's
// own telemetry/license clients, or another installation's discovery
// client during a real migration) needs it reachable unconditionally to
// be useful at all.
func RegisterRoutes(mux *http.ServeMux, publicURL string) {
	mux.HandleFunc("GET /.well-known/zonaryos-central", HandleWellKnown(publicURL))
}
