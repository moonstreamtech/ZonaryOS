// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Command server is the single deployable ZonaryOS backend binary.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/absence"
	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/ai"
	"github.com/moonstreamtech/ZonaryOS/internal/alerting"
	"github.com/moonstreamtech/ZonaryOS/internal/analytics"
	"github.com/moonstreamtech/ZonaryOS/internal/apidocs"
	"github.com/moonstreamtech/ZonaryOS/internal/apikey"
	"github.com/moonstreamtech/ZonaryOS/internal/apm"
	"github.com/moonstreamtech/ZonaryOS/internal/asset"
	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/budget"
	"github.com/moonstreamtech/ZonaryOS/internal/consolidation"
	"github.com/moonstreamtech/ZonaryOS/internal/contracts"
	"github.com/moonstreamtech/ZonaryOS/internal/costcenter"
	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/cryptutil"
	"github.com/moonstreamtech/ZonaryOS/internal/currency"
	"github.com/moonstreamtech/ZonaryOS/internal/discovery"
	"github.com/moonstreamtech/ZonaryOS/internal/documents"
	"github.com/moonstreamtech/ZonaryOS/internal/edgeagent"
	"github.com/moonstreamtech/ZonaryOS/internal/firm"
	"github.com/moonstreamtech/ZonaryOS/internal/health"
	"github.com/moonstreamtech/ZonaryOS/internal/helparticles"
	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/integration"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/invite"
	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
	"github.com/moonstreamtech/ZonaryOS/internal/license"
	"github.com/moonstreamtech/ZonaryOS/internal/localization"
	"github.com/moonstreamtech/ZonaryOS/internal/logistics"
	"github.com/moonstreamtech/ZonaryOS/internal/manufacturing"
	"github.com/moonstreamtech/ZonaryOS/internal/notification"
	"github.com/moonstreamtech/ZonaryOS/internal/notificationprefs"
	"github.com/moonstreamtech/ZonaryOS/internal/onboarding"
	"github.com/moonstreamtech/ZonaryOS/internal/payroll"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/config"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/httpapi"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/middleware"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/version"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
	"github.com/moonstreamtech/ZonaryOS/internal/plugin"
	"github.com/moonstreamtech/ZonaryOS/internal/plugins/activitylog"
	"github.com/moonstreamtech/ZonaryOS/internal/portability"
	"github.com/moonstreamtech/ZonaryOS/internal/procurement"
	"github.com/moonstreamtech/ZonaryOS/internal/project"
	"github.com/moonstreamtech/ZonaryOS/internal/ratelimit"
	"github.com/moonstreamtech/ZonaryOS/internal/reports"
	"github.com/moonstreamtech/ZonaryOS/internal/salesorders"
	"github.com/moonstreamtech/ZonaryOS/internal/scheduledreports"
	"github.com/moonstreamtech/ZonaryOS/internal/search"
	"github.com/moonstreamtech/ZonaryOS/internal/telemetry"
	"github.com/moonstreamtech/ZonaryOS/internal/timetracking"
	"github.com/moonstreamtech/ZonaryOS/internal/useractivity"
	"github.com/moonstreamtech/ZonaryOS/internal/warehouse"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// Structured JSON logging (this batch's observability foundation, see
// docs/DEVELOPMENT.md): every operational log line this binary and its
// sibling cmd/* tools emit goes through log/slog (stdlib, no new
// dependency) rather than the plain-text "log" package, so it's directly
// machine-parseable by whatever log aggregation an operator points at
// stdout. This is deliberately separate from internal/auditlog, which
// keeps recording business events (who did what, in which firm) to
// Postgres exactly as before - slog is for operational events (startup,
// request errors, unexpected states), not a replacement for the audit
// trail.
func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	// signal.NotifyContext (stdlib, Go 1.16+, no new dependency): ctx is
	// cancelled the moment this process receives SIGTERM or SIGINT -
	// every context derived from it below (schedulerCtx, reporterCtx,
	// ...) is cancelled at the same instant, and main's own final
	// graceful-shutdown sequence (srv.Shutdown + a final
	// metricsBuffer.FlushNow, see the bottom of this function) blocks on
	// this same ctx.Done() rather than on ListenAndServe's own blocking
	// call. This is genuinely new: before this batch, main() had no
	// signal handling at all - schedulerCtx/cancelScheduler's own
	// "stops together on shutdown" doc comments described an intended
	// behavior nothing actually triggered outside of a test calling
	// cancel directly, since ListenAndServe never returns on its own and
	// the process was simply killed out from under every goroutine.
	// Part 1's own "flush remaining [metrics] on SIGTERM" requirement is
	// what surfaced this gap - fixing it here benefits every other
	// scheduler in this file identically, not just internal/apm's own.
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopSignals()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	verifier, err := identity.NewVerifier(ctx, cfg.OIDCIssuerURL, cfg.OIDCClientID)
	if err != nil {
		slog.Error("init oidc verifier", "err", err)
		os.Exit(1)
	}
	// Wires API-key auth into every existing `identity.Middleware(verifier)`
	// call site across every module's own RegisterRoutes, with zero
	// changes to any of them - see identity.Verifier.Fallback's own doc
	// comment for why the extension point lives here.
	verifier.Fallback = &apikey.Fallback{Pool: pool}

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
		slog.Error("init license verifier", "err", err)
		os.Exit(1)
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

	// workflow.RunScheduler (this batch, Open Points item 7's partial
	// schedule-trigger support, scheduler.go): a lightweight goroutine
	// inside this same process - not a separate process, not a cron
	// daemon (per this batch's own brief) - polling every
	// workflow.SchedulerPollInterval for due scheduled_rule_runs across
	// every firm. Unconditional (no feature flag): unlike telemetry/
	// license, an installation with zero schedule-triggered rules simply
	// has nothing for each poll to find, so there is no meaningful
	// "disabled" state to gate here the way TelemetryEnabled/
	// LicenseEnforced gate their own goroutines. schedulerCtx is
	// cancelled on shutdown so this goroutine doesn't leak past the
	// server's own lifetime, same convention as reporterCtx above.
	schedulerCtx, cancelScheduler := context.WithCancel(ctx)
	defer cancelScheduler()
	go workflow.RunScheduler(schedulerCtx, pool, workflow.SchedulerPollInterval)

	// asset.RunMaintenanceScheduler (asset management/maintenance/facility
	// operations batch): a second, dedicated background goroutine, same
	// shape as workflow.RunScheduler above but its own file/package - see
	// internal/asset/scheduler.go's own doc comment for why this is a new
	// goroutine rather than an extension of scheduled_rule_runs. Shares
	// schedulerCtx/cancelScheduler so both stop together on shutdown.
	go asset.RunMaintenanceScheduler(schedulerCtx, pool, asset.MaintenanceSchedulerPollInterval)

	// contracts.RunExpiryScheduler (contracts management/document
	// workflows/legal foundation batch): a third, dedicated background
	// goroutine, same shape as asset.RunMaintenanceScheduler above - see
	// internal/contracts/scheduler.go's own doc comment for why this is a
	// new goroutine rather than reusing scheduled_rule_runs. Shares
	// schedulerCtx/cancelScheduler so all three stop together on shutdown.
	go contracts.RunExpiryScheduler(schedulerCtx, pool, contracts.ExpirySchedulerPollInterval)

	// scheduledreports.RunScheduler (analytics/BI/advanced reporting
	// batch, Part 4): same RunX(ctx, pool, pollInterval)/ProcessX(ctx, pool)
	// shape as the other schedulers above - unconditional (a fast, cheap
	// per-firm no-op for any firm with no due scheduled report), shares
	// schedulerCtx/cancelScheduler so it stops together with the rest.
	go scheduledreports.RunScheduler(schedulerCtx, pool, scheduledreports.DefaultSchedulerPollInterval)

	// edgeagent.NewNATSBridge (NATS JetStream/Edge Agent completion
	// batch, Part 1): default-off, gated by cfg.NATSURL
	// (ZONARYOS_NATS_URL) - see that function's own doc comment. Unlike
	// simply leaving NATS unset (the nil, nil case, silently a no-op), a
	// connection failure when NATS IS configured is a real startup error:
	// an operator explicitly opted in, and failing loudly here is better
	// than an installation that appears to run but silently never
	// consumes the queue it was configured to consume from.
	natsBridge, err := edgeagent.NewNATSBridge(ctx, cfg.NATSURL)
	if err != nil {
		slog.Error("failed to connect to NATS", "err", err)
		os.Exit(1)
	}
	defer natsBridge.Close()
	if natsBridge != nil {
		natsCtx, cancelNATS := context.WithCancel(ctx)
		defer cancelNATS()
		go edgeagent.RunSubscriber(natsCtx, natsBridge, pool, cfg.NATSPollInterval)
	}

	// internal/ai.NewEncryptor (AI integration layer batch): default-off,
	// gated by cfg.AIEncryptionKey (ZONARYOS_AI_ENCRYPTION_KEY) - see that
	// function's own doc comment. An operator who set the value but got it
	// wrong (bad base64, wrong length) fails startup loudly, the same
	// "unset is fine, set-but-wrong is not" posture
	// edgeagent.NewNATSBridge's own comment above describes for NATSURL.
	aiEncryptor, err := ai.NewEncryptor(cfg.AIEncryptionKey)
	if err != nil {
		slog.Error("init AI encryptor", "err", err)
		os.Exit(1)
	}
	// ai.RunAnomalyScheduler (Part 4): a fourth, dedicated background
	// goroutine, same RunX(ctx, pool, pollInterval)/ProcessX(ctx, pool)
	// shape as asset.RunMaintenanceScheduler/contracts.RunExpiryScheduler
	// above - unconditional (it is a fast per-firm no-op for any firm with
	// no active AI configuration, see ProcessAnomalyChecks), shares
	// schedulerCtx/cancelScheduler so all four stop together on shutdown.
	go ai.RunAnomalyScheduler(schedulerCtx, pool, aiEncryptor, cfg.AIAnomalySchedulerPollInterval)

	// internal/integration (data pipeline/ETL/system integrations batch):
	// integrationEncryptor, default-off, gated by
	// cfg.IntegrationEncryptionKey (ZONARYOS_INTEGRATION_ENCRYPTION_KEY) -
	// a SEPARATE key from aiEncryptor's own ZONARYOS_AI_ENCRYPTION_KEY, see
	// that field's own doc comment in internal/platform/config. Same
	// "unset is fine, set-but-wrong is not" startup posture as aiEncryptor
	// above.
	integrationEncryptor, err := cryptutil.NewEncryptor(cfg.IntegrationEncryptionKey, "ZONARYOS_INTEGRATION_ENCRYPTION_KEY")
	if err != nil {
		slog.Error("init integration encryptor", "err", err)
		os.Exit(1)
	}
	// inventory.SetOutboundDispatcher/crm.SetOutboundDispatcher wire the
	// func-var hook pattern (see those packages' own outbound_hook.go)
	// to internal/integration.DispatchOutbound - internal/inventory and
	// internal/crm cannot import internal/integration directly (it would
	// complete an import cycle back through internal/invoicing/
	// internal/workflow), so each package instead calls an internal,
	// package-level func-var that defaults to a no-op until wired here,
	// once, at startup.
	inventory.SetOutboundDispatcher(integration.DispatchOutbound)
	crm.SetOutboundDispatcher(integration.DispatchOutbound)
	// integration.SetEncryptor wires the package-level pkgEncryptor
	// DispatchOutbound's own deliverOutboundOne needs to decrypt a
	// connector's config - see that function's own doc comment for why
	// it can't just take an explicit parameter like RegisterRoutes below
	// does.
	integration.SetEncryptor(integrationEncryptor)
	// integration.RunInboundScheduler: a fifth, dedicated background
	// goroutine, same RunX(ctx, pool, pollInterval)/ProcessX(ctx, pool)
	// shape as the other schedulers above - unconditional (it is a fast
	// per-firm, per-connector no-op for any firm with no active
	// http_webhook connector holding an inbound mapping, see
	// ProcessInboundSyncs), shares schedulerCtx/cancelScheduler so all
	// five stop together on shutdown.
	go integration.RunInboundScheduler(schedulerCtx, pool, integrationEncryptor, cfg.IntegrationInboundPollInterval)

	// internal/apm.Buffer (performance monitoring/alerting/operational
	// excellence batch, Part 1): the in-memory, goroutine-safe request-
	// metrics accumulator middleware.MetricsRecorder feeds on every
	// request - see that package's own doc comment for the buffering
	// design (30s-or-100-records flush, whichever first). Wired here,
	// once, at startup - the same func-var hook pattern
	// inventory.SetOutboundDispatcher establishes above, needed because
	// internal/platform/middleware cannot import internal/apm directly
	// (see MetricsRecorder's own doc comment).
	metricsBuffer := apm.NewBuffer(pool)
	middleware.MetricsRecorder = metricsBuffer.Record
	// RunFlushLoop's own periodic tick shares schedulerCtx like every
	// other scheduler above, but - unlike them - also performs one final
	// flush the instant schedulerCtx is cancelled (see that method's own
	// doc comment), which is what actually delivers Part 1's own
	// "flush remaining on SIGTERM" requirement; main's own final
	// srv.Shutdown sequence below waits for this goroutine to finish
	// via schedulerDone before the process exits, so that final flush is
	// guaranteed to complete, not just be scheduled.
	schedulerDone := make(chan struct{})
	go func() {
		metricsBuffer.RunFlushLoop(schedulerCtx)
		close(schedulerDone)
	}()
	// apm.RunSelfHealingScheduler (Part 4): partition creation (genuine
	// automatic remediation) and stale-lock detection (monitoring-only,
	// see that function's own doc comment for why it never auto-kills a
	// query) - same RunX/ProcessX shape as every other scheduler here.
	go apm.RunSelfHealingScheduler(schedulerCtx, pool, apm.DefaultSelfHealingPollInterval)
	// alerting.RunChecker (Part 2): evaluates active alert_rules every 5
	// minutes against internal/apm's own aggregate queries - see that
	// package's own doc comment. cfg.PlatformAdminEmails is the same
	// allowlist source platformAdminAllowlist (below) is built from; a
	// breach notifies every one of those emails via a structured log
	// line (Open Points item 17 - email is still deferred, see
	// internal/alerting's own doc comment on notifyPlatformAdmins).
	go alerting.RunChecker(schedulerCtx, pool, cfg.PlatformAdminEmails, alerting.DefaultCheckerPollInterval)

	// internal/ratelimit.Limiter (tenant-isolation-hardening/security
	// batch, Part 2): built unconditionally (the five cfg.RateLimit*
	// fields all have sensible positive defaults, see
	// internal/platform/config.Load) - there is no "rate limiting off"
	// state the way license/telemetry have, since unlike those, an
	// installation always benefits from a baseline flood ceiling. Wired
	// into middleware.Chain below (via ratelimit.Middleware(rateLimiter)),
	// not called directly here - see that package's own doc comment.
	// RunLogFlushLoop's own aggregate-not-per-request rejection logging
	// shares schedulerCtx like every other background goroutine above.
	rateLimiter := ratelimit.NewLimiter(ratelimit.Limits{
		Standard: cfg.RateLimitStandardPerMinute, Bulk: cfg.RateLimitBulkPerMinute,
		AI: cfg.RateLimitAIPerMinute, Export: cfg.RateLimitExportPerMinute,
		AnalyticsIngest: cfg.RateLimitAnalyticsIngestPerMinute,
	})
	go ratelimit.RunLogFlushLoop(schedulerCtx, rateLimiter)

	// internal/plugin.Registry (plugin/extension architecture + developer
	// API/SDK foundation batch): built once, here, before any route is
	// registered or any request served - Part 2's own "immutable after
	// init, no hot-reloading" scope boundary. internal/plugins/activitylog
	// (Part 3's real example plugin) registers itself as both a
	// WorkflowHook and a KPIProvider; a future plugin would add one more
	// line here, nothing else in this file.
	pluginRegistry := plugin.NewRegistry()
	activitylog.Register(pluginRegistry, pool)
	// Bridging each extension point into the core package that actually
	// dispatches to it - see internal/workflow/plugin_hooks.go's and
	// internal/reports/external.go's own doc comments for why this
	// bridging happens here (in main.go) rather than internal/plugin
	// calling into internal/workflow/internal/reports directly (an
	// import-cycle concern: internal/plugin already imports
	// internal/reports, which imports internal/workflow).
	for _, hook := range pluginRegistry.WorkflowHooks() {
		workflow.RegisterTransitionHook(hook)
	}
	for _, provider := range pluginRegistry.KPIProviders() {
		reports.RegisterExternalKPIProvider(provider)
	}
	for _, source := range pluginRegistry.ReportSources() {
		reports.RegisterExternalSource(source)
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
	// internal/payroll + internal/timetracking + internal/absence (this
	// batch): payroll foundation, time tracking, and HR depth
	// (absences) - all built on internal/hr's people/contracts core.
	payroll.RegisterRoutes(mux, verifier, pool)
	timetracking.RegisterRoutes(mux, verifier, pool)
	absence.RegisterRoutes(mux, verifier, pool)
	inventory.RegisterRoutes(mux, verifier, pool)
	logistics.RegisterRoutes(mux, verifier, pool)
	crm.RegisterRoutes(mux, verifier, pool)
	project.RegisterRoutes(mux, verifier, pool)
	invoicing.RegisterRoutes(mux, verifier, pool)
	salesorders.RegisterRoutes(mux, verifier, pool)
	procurement.RegisterRoutes(mux, verifier, pool)
	manufacturing.RegisterRoutes(mux, verifier, pool)
	warehouse.RegisterRoutes(mux, verifier, pool)
	asset.RegisterRoutes(mux, verifier, pool)
	contracts.RegisterRoutes(mux, verifier, pool)
	budget.RegisterRoutes(mux, verifier, pool)
	costcenter.RegisterRoutes(mux, verifier, pool)
	portability.RegisterRoutes(mux, verifier, pool)
	reports.RegisterRoutes(mux, verifier, pool)
	// internal/analytics + internal/scheduledreports (analytics/BI/
	// advanced reporting batch): event ingestion/time-series/top-by-
	// property queries, and owner-gated CRUD for scheduled_reports - see
	// scheduledreports' own package doc comment for why it's a separate
	// package from internal/reports/internal/notification rather than
	// coupling either of those two together.
	analytics.RegisterRoutes(mux, verifier, pool)
	scheduledreports.RegisterRoutes(mux, verifier, pool)
	documents.RegisterRoutes(mux, verifier, pool)
	apikey.RegisterRoutes(mux, verifier, pool)
	webhook.RegisterRoutes(mux, verifier, pool)
	search.RegisterRoutes(mux, verifier, pool)
	// internal/onboarding + internal/helparticles (user experience
	// improvements/onboarding flow/help system batch): first-run
	// onboarding checklist progress and the contextual help article
	// library - see their own package doc comments.
	onboarding.RegisterRoutes(mux, verifier, pool)
	helparticles.RegisterRoutes(mux, verifier, pool)
	// platformAdminAllowlist is shared between internal/platformadmin's
	// own routes and internal/currency's platform-admin-only
	// POST /api/exchange-rates - one allowlist, the same "which real
	// ZonaryOS-the-company staff emails" config value, not a second
	// independent gate.
	platformAdminAllowlist := platformadmin.NewAllowlist(cfg.PlatformAdminEmails)
	platformadmin.RegisterRoutes(mux, verifier, pool, platformAdminAllowlist, licenseVerifier)
	// internal/apm + internal/alerting (performance monitoring/alerting/
	// operational excellence batch): GET /api/platform-admin/metrics/summary
	// and the alert-rules CRUD + GET /api/platform-admin/alerts - same
	// allowlist as every other platform-admin route group, no separate
	// gate.
	apm.RegisterRoutes(mux, verifier, pool, platformAdminAllowlist)
	alerting.RegisterRoutes(mux, verifier, pool, platformAdminAllowlist)
	// internal/currency (this batch): the multi-currency foundation -
	// platform-admin-gated rate creation, unauthenticated rate lookup.
	currency.RegisterRoutes(mux, verifier, pool, platformAdminAllowlist)
	// internal/consolidation (multi-company/consolidation/group-accounting
	// batch): firm groups, inter-company transfers, and consolidated P&L -
	// platform-admin-gated writes/reads, same allowlist, plus one
	// firm-scoped member-gated read (GET /api/firms/{firmID}/group).
	consolidation.RegisterRoutes(mux, verifier, pool, platformAdminAllowlist)
	// internal/localization: owner-gated, firm-scoped CRUD for addresses
	// and tax rates, plus (multi-language UI/localization depth/fiscal
	// compliance batch, Part 3) platform-wide fiscal country reference
	// data - unauthenticated reads, platform-admin-gated writes, same
	// allowlist as internal/currency's own platform-admin-only route.
	localization.RegisterRoutes(mux, verifier, pool, platformAdminAllowlist)
	// internal/plugin (plugin/extension architecture + developer API/SDK
	// foundation batch, Part 1): the plugins catalog (unauthenticated
	// reads, platform-admin-gated writes, same allowlist) plus owner-
	// gated, firm-scoped plugin configuration.
	plugin.RegisterRoutes(mux, verifier, pool, platformAdminAllowlist)
	// internal/apidocs (same batch, Part 4): GET /api/docs serves
	// docs/api/openapi.yaml raw - unauthenticated, no route-registration
	// dependency on anything else in this file.
	apidocs.RegisterRoutes(mux)
	// internal/edgeagent (this batch, Vision §9's Edge Agent protocol
	// foundation): registers both the ordinary Keycloak-authenticated
	// firm-scoped routes (register/list agents, issue/list tokens, list
	// events) and the token-authenticated /api/edge/* routes the agent
	// itself calls - see that package's own doc comment for why these
	// are two entirely separate auth chains.
	edgeagent.RegisterRoutes(mux, verifier, pool)
	// internal/ai (AI integration layer + intelligent automation batch,
	// Vision §5): AI configuration CRUD plus the two AI-suggestion
	// endpoints - see that package's doc comment for the Part 5 safety
	// constraints (opt-in, 404 not 403 when unconfigured, PII firewall).
	// aiEncryptor may be nil (ZONARYOS_AI_ENCRYPTION_KEY unset) - every
	// handler passes it straight through and fails with a clear 500
	// (ErrEncryptionKeyNotSet) rather than the routes not existing at all.
	ai.RegisterRoutes(mux, verifier, pool, aiEncryptor)
	// internal/integration (data pipeline/ETL/system integrations batch):
	// connector/mapping/sync-log CRUD, owner-gated, firm-scoped - see that
	// package's own doc comment. integrationEncryptor may be nil
	// (ZONARYOS_INTEGRATION_ENCRYPTION_KEY unset) - every handler passes
	// it straight through and fails with a clear 500
	// (ErrEncryptionKeyNotSet) rather than the routes not existing at all,
	// same posture as ai.RegisterRoutes above.
	integration.RegisterRoutes(mux, verifier, pool, integrationEncryptor)
	// internal/notification (this batch): the in-app notification inbox
	// - GET .../notifications, GET .../notifications/unread-count,
	// PATCH .../notifications/{id}/read. Unconditional, same as every
	// other member-gated route group - see that package's own doc
	// comment for its scope boundaries (no email/push, no WebSocket
	// push).
	notification.RegisterRoutes(mux, verifier, pool)
	notificationprefs.RegisterRoutes(mux, verifier, pool)
	// GET .../activity: the dashboard's recent-user-activity widget
	// (Issue #59) - member-gated, read-only, paginated/filterable. See
	// internal/useractivity's own doc comment for how this differs from
	// internal/auditlog's compliance-gated trail.
	useractivity.RegisterRoutes(mux, verifier, pool)
	// Only actually registers the two /telemetry/* endpoints when
	// telemetryReporter is non-nil (enabled) - see
	// telemetry.RegisterRoutes's own comment: a disabled installation
	// has no listening surface for this package at all, not just
	// no-op handlers.
	telemetry.RegisterRoutes(mux, telemetryReporter)
	// GET /health (this batch's observability foundation): unauthenticated,
	// checks Postgres and Keycloak in parallel - see internal/health's own
	// doc comment for why this is distinct from httpapi's existing plain
	// liveness-only GET /healthz.
	mux.HandleFunc("GET /health", health.Handler(pool, health.OIDCDiscoveryURL(cfg.OIDCIssuerURL), nil))
	// GET /metrics: a stub only, same posture as internal/license's own
	// default-disabled surfaces - real metrics (request latencies,
	// counts, etc.) are not exposed at all yet (no metrics backend is
	// wired up), and this deliberately never returns anything but 401 so
	// no future accidental change here starts leaking operational data
	// to an unauthenticated caller by surprise. See docs/DEVELOPMENT.md.
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})

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
	//
	// middleware.Chain (server infrastructure hardening batch, Part 4)
	// wraps OUTSIDE telemetry.Middleware, unconditionally - request ID/
	// panic recovery/size limits/timeout are not default-off features
	// the way license/telemetry are; they're baseline hardening every
	// installation gets. PanicRecovery being outermost-but-one (only
	// RequestID wraps it) means it also protects telemetry.Middleware
	// itself, not just route handlers - see middleware.Chain's own doc
	// comment for the full ordering rationale.
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           middleware.Chain(telemetry.Middleware(telemetryReporter, mux)(mux), cfg.RequestTimeout, cfg.MaxJSONBodyBytes, cfg.MaxUploadBodyBytes, ratelimit.Middleware(rateLimiter)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// srv.ListenAndServe runs in its own goroutine so main can instead
	// block on ctx.Done() (SIGTERM/SIGINT, see the signal.NotifyContext
	// call above) and drive an explicit, ordered shutdown: stop
	// accepting new requests first (srv.Shutdown, which itself waits for
	// in-flight requests to finish), THEN cancel every background
	// scheduler (cancelScheduler), THEN wait for metricsBuffer's own
	// final flush to actually complete (schedulerDone) before the
	// process exits - so a metric for the very last request this server
	// handled is never silently dropped.
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("ZonaryOS server listening", "addr", cfg.HTTPAddr, "version", version.Version)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("server stopped", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining in-flight requests")
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown", "err", err)
		}
		cancelShutdown()
		cancelScheduler()
		<-schedulerDone
		slog.Info("shutdown complete")
	}
}
