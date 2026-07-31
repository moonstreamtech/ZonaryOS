// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Command server is the single deployable ZonaryOS backend binary.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/firm"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/invite"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/config"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/httpapi"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
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

	broadcaster := permission.NewBroadcaster()

	mux := httpapi.NewMux()
	identity.RegisterRoutes(mux, verifier, pool)
	workflow.RegisterRoutes(mux, verifier, pool)
	wizard.RegisterRoutes(mux, verifier, pool)
	permission.RegisterRoutes(mux, verifier, pool, broadcaster)
	auditlog.RegisterRoutes(mux, verifier, pool)
	firm.RegisterRoutes(mux, verifier, pool)
	invite.RegisterRoutes(mux, verifier, pool)
	platformadmin.RegisterRoutes(mux, verifier, pool, platformadmin.NewAllowlist(cfg.PlatformAdminEmails))

	// An explicit http.Server (not the bare http.ListenAndServe function)
	// so ReadHeaderTimeout can be set - a slowloris mitigation (CI
	// Checklist item 11's SAST scan, gosec G114, flags the bare function
	// for exactly this reason). WriteTimeout/ReadTimeout are deliberately
	// left unset: internal/permission's SSE endpoint
	// (GET .../permission-events) intentionally keeps its response stream
	// open indefinitely, and a blanket WriteTimeout would force-close it.
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("ZonaryOS server listening on %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server: %v", err)
	}
}
