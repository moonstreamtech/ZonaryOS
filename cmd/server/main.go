// Command server is the single deployable ZonaryOS backend binary.
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/config"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/httpapi"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer pool.Close()

	verifier, err := identity.NewVerifier(ctx, cfg.OIDCIssuerURL, cfg.OIDCClientID)
	if err != nil {
		log.Fatalf("init oidc verifier: %v", err)
	}

	mux := httpapi.NewMux()
	identity.RegisterRoutes(mux, verifier, pool)
	workflow.RegisterRoutes(mux, verifier, pool)

	log.Printf("ZonaryOS server listening on %s", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
