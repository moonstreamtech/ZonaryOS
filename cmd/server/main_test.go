package main

import (
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/httpapi"
	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// TestRegisterRoutes_DoesNotPanic builds the exact same mux main() does
// (minus the network/DB dials NewVerifier and db.Open need, which
// route registration itself never touches) and asserts it doesn't panic.
// net/http.ServeMux.Handle panics at registration time - not on first
// request - when two patterns are ambiguous (neither more specific than
// the other at every differing segment); that bug only surfaced by
// actually starting the server once, which this test now catches
// without needing Postgres or Keycloak.
func TestRegisterRoutes_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registering routes panicked: %v", r)
		}
	}()

	verifier := new(identity.Verifier)
	mux := httpapi.NewMux()
	identity.RegisterRoutes(mux, verifier, nil)
	workflow.RegisterRoutes(mux, verifier, nil)
	wizard.RegisterRoutes(mux, verifier, nil)
}
