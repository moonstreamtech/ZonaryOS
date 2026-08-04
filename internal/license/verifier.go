// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package license

import (
	"crypto/ed25519"
	"fmt"
	"sync"
	"time"
)

// DefaultGracePeriod is used when ZONARYOS_LICENSE_GRACE_PERIOD is unset.
//
// PROVISIONAL DEFAULT, NOT A DECIDED SLA: docs/OPEN_POINTS.md item 32's
// open question 2 ("will verification halt the system when offline, or
// is there a grace period?") is explicitly still open - no concrete
// duration has been approved by the Developer. 72 hours is hardcoded here
// only because *something* has to ship before item 32 is finalized, and
// because it's a defensible starting guess (long enough to survive a
// weekend/a delayed renewal without an outage, short enough that a
// genuinely lapsed license doesn't run unenforced for weeks). Matches
// deploy/backup/backup-postgres.sh's own "PROVISIONAL DEFAULT" precedent
// for docs/OPEN_POINTS.md item 25 - same situation, same honesty about it.
const DefaultGracePeriod = 72 * time.Hour

// RecheckInterval is how often cmd/server/main.go's background goroutine
// calls Verifier.Check when enforcement is enabled.
//
// Why periodic rather than startup-only: this token is static local
// config today (ZONARYOS_LICENSE_TOKEN), not fetched from a live central
// server (item 34's real issuance/verification infrastructure doesn't
// exist yet) - so periodic re-checking isn't about detecting a revoked
// license over the network yet. What it does catch, on a long-running
// server process, is the token's own expiry (plus grace period) coming
// due *while the process is already running* - without this, a server
// started with a valid token would keep treating it as valid forever,
// even hours or days past its real expiry, until the next restart forced
// Check to run again. 15 minutes is a judgment call, not a tuned number
// (item 32's open question 1, "exactly when/how will verification
// trigger," is explicitly still open) - frequent enough that an
// expired/lapsed license is caught within a reasonable window, infrequent
// enough to be obviously negligible cost (this is a local signature
// re-check on an already-decoded token, not a network call - there is
// nothing to rate-limit against yet).
const RecheckInterval = 15 * time.Minute

// Verifier is the stateful, running check this codebase asks at each
// gated call site: "is the current license good enough to allow this
// module?" Constructed once at startup (NewVerifier, called from
// cmd/server/main.go) and shared by every call site that gates on it.
//
// # The default-disabled guarantee
//
// A Verifier built with enforced=false (i.e. ZONARYOS_LICENSE_ENFORCEMENT
// is unset or not exactly "true" - see internal/platform/config) never
// parses a public key, never touches ZONARYOS_LICENSE_TOKEN, and never
// runs a signature check. Allow, its one gating method, returns nil as
// its literal first statement whenever the Verifier is nil or disabled -
// see Allow's own comment. This is intentionally not "usually returns nil
// when disabled" - it is structurally guaranteed by that first line, the
// same "check the one thing that matters before anything else can run"
// shape internal/platformadmin.ListFirms already uses for its own
// allowlist check. Every existing call path that does not explicitly opt
// into ZONARYOS_LICENSE_ENFORCEMENT=true is therefore completely
// unaffected by this package's existence: zero new checks run, zero new
// latency, zero new failure modes.
type Verifier struct {
	enforced    bool
	publicKey   ed25519.PublicKey
	rawToken    string
	gracePeriod time.Duration

	mu       sync.RWMutex
	token    Token
	tokenErr error
}

// NewVerifier builds a Verifier from already-loaded config
// (internal/platform/config.Config's License* fields - this package
// intentionally takes plain values, not a dependency on the config
// package itself, so it stays usable standalone in tests).
//
// If enforced is false, every other parameter is ignored entirely and a
// disabled Verifier is returned immediately - no parsing, no validation,
// so a deployment that never sets ZONARYOS_LICENSE_ENFORCEMENT=true is
// never required to configure a public key or a token either. If
// enforced is true but publicKeyRaw is not a valid Ed25519 public key,
// this returns an error (a real startup misconfiguration - enforcement
// was explicitly requested but can never succeed) rather than silently
// falling back to disabled. gracePeriod of zero is replaced with
// DefaultGracePeriod.
func NewVerifier(enforced bool, publicKeyRaw, rawToken string, gracePeriod time.Duration) (*Verifier, error) {
	if !enforced {
		return &Verifier{enforced: false}, nil
	}
	if gracePeriod <= 0 {
		gracePeriod = DefaultGracePeriod
	}
	pub, err := ParsePublicKey(publicKeyRaw)
	if err != nil {
		return nil, fmt.Errorf("ZONARYOS_LICENSE_ENFORCEMENT=true but ZONARYOS_LICENSE_PUBLIC_KEY is not a valid Ed25519 public key: %w", err)
	}
	v := &Verifier{
		enforced:    true,
		publicKey:   pub,
		rawToken:    rawToken,
		gracePeriod: gracePeriod,
	}
	v.Check()
	return v, nil
}

// Check re-parses and re-verifies the configured token's signature and
// caches the result (Token and/or error) for Allow to read. Called once
// synchronously by NewVerifier and then periodically by
// cmd/server/main.go's ticker goroutine (RecheckInterval) - see that
// const's comment for why periodic re-checking matters even though the
// token itself is static config. A no-op (returns immediately) on a nil
// or disabled Verifier, matching Allow's own guarantee.
func (v *Verifier) Check() {
	if v == nil || !v.enforced {
		return
	}
	var tok Token
	var err error
	if v.rawToken == "" {
		err = fmt.Errorf("%w: no license token is configured", ErrDenied)
	} else {
		tok, err = Verify(v.rawToken, v.publicKey)
		if err != nil {
			err = fmt.Errorf("%w: %v", ErrDenied, err)
		}
	}
	v.mu.Lock()
	v.token, v.tokenErr = tok, err
	v.mu.Unlock()
}

// Allow is this package's single access-control decision: may moduleKey
// be used right now? Returns nil to allow, or a non-nil error (always
// wrapping ErrDenied, with a specific, actionable message - see below)
// to deny. This is the one function every gated call site should call.
//
// The first statement is the entire default-disabled guarantee described
// on Verifier's doc comment - deliberately the very first line, not
// buried after other logic, so it's obvious from reading the function
// that a nil or disabled Verifier can never deny anything: there is
// nothing after this line for that case to reach.
func (v *Verifier) Allow(moduleKey string) error {
	if v == nil || !v.enforced {
		return nil
	}

	v.mu.RLock()
	tok, tokenErr, grace := v.token, v.tokenErr, v.gracePeriod
	v.mu.RUnlock()

	if tokenErr != nil {
		return fmt.Errorf("%w: license enforcement is enabled (ZONARYOS_LICENSE_ENFORCEMENT=true) but no valid license token is configured (%v) - set ZONARYOS_LICENSE_TOKEN to a token issued by cmd/licensegen (test/dev - see docs/DEVELOPMENT.md) or a real ZonaryOS license, or set ZONARYOS_LICENSE_ENFORCEMENT=false to disable enforcement", ErrDenied, tokenErr)
	}

	if cutoff := tok.ExpiresAt.Add(grace); time.Now().After(cutoff) {
		return fmt.Errorf("%w: the license token for %q expired at %s and its %s offline grace period (ZONARYOS_LICENSE_GRACE_PERIOD) has also elapsed - obtain a renewed token (cmd/licensegen for test/dev, or contact ZonaryOS licensing for a real one) and update ZONARYOS_LICENSE_TOKEN",
			ErrDenied, tok.IssuedTo, tok.ExpiresAt.UTC().Format(time.RFC3339), grace)
	}

	if !containsModule(tok.Modules, moduleKey) {
		return fmt.Errorf("%w: module %q is not included in the active license's module list (%v) - contact ZonaryOS licensing to enable it, or reissue a test token with cmd/licensegen's -modules flag including %q",
			ErrDenied, moduleKey, tok.Modules, moduleKey)
	}

	return nil
}

// IsModuleActive is Allow's boolean-shaped convenience form, for call
// sites that only need a yes/no answer rather than the actionable error
// text (e.g. deciding whether to render a UI affordance). Delegates to
// Allow so there is exactly one source of truth for the decision.
func (v *Verifier) IsModuleActive(moduleKey string) bool {
	return v.Allow(moduleKey) == nil
}

func containsModule(modules []string, key string) bool {
	for _, m := range modules {
		if m == key {
			return true
		}
	}
	return false
}
