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

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/discovery"
	"github.com/moonstreamtech/ZonaryOS/internal/firm"
	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/invite"
	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
	"github.com/moonstreamtech/ZonaryOS/internal/license"
	"github.com/moonstreamtech/ZonaryOS/internal/logistics"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/config"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/httpapi"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
	"github.com/moonstreamtech/ZonaryOS/internal/reports"
	"github.com/moonstreamtech/ZonaryOS/internal/telemetry"
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

	// internal/license.Verifier (Open Points item 32). Built unconditionally
	// (not skipped when disabled) so every call site can rely on it being
	// non-nil - but NewVerifier(false, ...) never parses cfg.LicensePublicKey
	// or cfg.LicenseToken at all when cfg.LicenseEnforced is false (the
	// default), so an operator who never sets ZONARYOS_LICENSE_ENFORCEMENT=true
	// never needs to configure either of those either. See
	// internal/license.Verifier's doc comment for the structural
	// default-disabled guarantee this rests on.
	licenseVerifier, err := license.NewVerifier(cfg.LicenseEnforced, cfg.LicensePublicKey, cfg.LicenseToken, cfg.LicenseGracePeriod)
	if err != nil {
		log.Fatalf("license: %v", err)
	}
	// The periodic re-check goroutine only exists at all when enforcement
	// is actually enabled - not merely a no-op tick when disabled, an
	// entirely absent goroutine, so a deployment that never opts in pays
	// zero background-goroutine cost from this package either. See
	// license.RecheckInterval's doc comment for why periodic re-checking
	// (rather than startup-only) matters even though the token itself is
	// static local config today.
	if cfg.LicenseEnforced {
		go func() {
			ticker := time.NewTicker(license.RecheckInterval)
			defer ticker.Stop()
			for range ticker.C {
				licenseVerifier.Check()
			}
		}()
	}

	// internal/discovery.Client (Open Points item 34): the single shared
	// "where is the central server right now" resolver both this batch's
	// internal/telemetry (below) and a future network-aware
	// internal/license both use, so there is exactly one HTTP-fetch-
	// with-cache implementation, not one per consumer - see that
	// package's doc comment. Built unconditionally: it is a plain,
	// stateless-until-called struct (no goroutine, no network call
	// happens here), so building it costs nothing even on an
	// installation that never enables telemetry and whose license
	// verification never ends up needing a network call either.
	discoveryClient := discovery.New(cfg.DiscoveryStartURL)

	// internal/telemetry.Reporter (this batch, Open Points item 40 - see
	// docs/OPEN_POINTS.md). NewReporter(false, ...) returns a literal
	// nil when cfg.TelemetryEnabled is false (the default) - not a
	// disabled-but-allocated struct - so every telemetry.Record*/
	// Middleware call below is a true no-op on a nil receiver. See
	// internal/telemetry.Reporter's doc comment for the same structural
	// "default-disabled guarantee" internal/license.Verifier's doc
	// comment establishes for its own package.
	telemetryReporter := telemetry.NewReporter(cfg.TelemetryEnabled, discoveryClient)
	// The periodic flush goroutine only exists at all when telemetry is
	// actually enabled - not merely idling, an entirely absent
	// goroutine, matching license.RecheckInterval's own goroutine-
	// gating precedent exactly. reporterCtx is cancelled when srv shuts
	// down (deferred below) so this goroutine doesn't leak past the
	// server's own lifetime.
	reporterCtx, cancelReporter := context.WithCancel(ctx)
	defer cancelReporter()
	if cfg.TelemetryEnabled {
		go telemetryReporter.Start(reporterCtx)
	}

	mux := httpapi.NewMux()
	// The well-known discovery endpoint (Open Points item 34) is
	// unconditional - see internal/discovery.RegisterRoutes's own
	// comment for why it has no default-off gate unlike license/
	// telemetry.
	discovery.RegisterRoutes(mux, cfg.PublicURL)
	identity.RegisterRoutes(mux, verifier, pool)
	workflow.RegisterRoutes(mux, verifier, pool)
	wizard.RegisterRoutes(mux, verifier, pool)
	permission.RegisterRoutes(mux, verifier, pool, broadcaster)
	auditlog.RegisterRoutes(mux, verifier, pool)
	firm.RegisterRoutes(mux, verifier, pool)
	invite.RegisterRoutes(mux, verifier, pool)
	accounting.RegisterRoutes(mux, verifier, pool)
	hr.RegisterRoutes(mux, verifier, pool)
	inventory.RegisterRoutes(mux, verifier, pool)
	logistics.RegisterRoutes(mux, verifier, pool)
	crm.RegisterRoutes(mux, verifier, pool)
	invoicing.RegisterRoutes(mux, verifier, pool)
	reports.RegisterRoutes(mux, verifier, pool)
	platformadmin.RegisterRoutes(mux, verifier, pool, platformadmin.NewAllowlist(cfg.PlatformAdminEmails), licenseVerifier)
	// Only actually registers the two /telemetry/* endpoints when
	// telemetryReporter is non-nil (enabled) - see
	// telemetry.RegisterRoutes's own comment: a disabled installation
	// has no listening surface for this package at all, not just
	// no-op handlers.
	telemetry.RegisterRoutes(mux, telemetryReporter)

	// An explicit http.Server (not the bare http.ListenAndServe function)
	// so ReadHeaderTimeout can be set - a slowloris mitigation (CI
	// Checklist item 11's SAST scan, gosec G114, flags the bare function
	// for exactly this reason). WriteTimeout/ReadTimeout are deliberately
	// left unset: internal/permission's SSE endpoint
	// (GET .../permission-events) intentionally keeps its response stream
	// open indefinitely, and a blanket WriteTimeout would force-close it.
	//
	// telemetry.Middleware(telemetryReporter, mux) wraps the whole mux
	// with per-request/source-IP counting - on a nil telemetryReporter
	// (the default) this returns mux itself, completely unwrapped (see
	// that function's own comment), so Handler below is byte-for-byte
	// what it was before this batch on any installation that hasn't
	// opted in.
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           telemetry.Middleware(telemetryReporter, mux)(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("ZonaryOS server listening on %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server: %v", err)
	}
}
