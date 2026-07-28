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
