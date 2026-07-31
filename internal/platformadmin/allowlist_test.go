// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package platformadmin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

func TestAllowlist_Contains_IsCaseAndWhitespaceInsensitive(t *testing.T) {
	allow := platformadmin.NewAllowlist([]string{"  Admin@Zonaryos.com "})

	if !allow.Contains("admin@zonaryos.com") {
		t.Error("expected lower-cased exact match to be contained")
	}
	if !allow.Contains("  ADMIN@ZONARYOS.COM  ") {
		t.Error("expected differently-cased/whitespace-padded email to still match")
	}
	if allow.Contains("someone-else@zonaryos.com") {
		t.Error("expected an unrelated email not to be contained")
	}
}

func TestAllowlist_Empty_ContainsNothing(t *testing.T) {
	allow := platformadmin.NewAllowlist(nil)
	if allow.Contains("anyone@zonaryos.com") {
		t.Error("expected an empty allowlist to reject everyone - deny by default")
	}
}

// TestListFirms_RejectsNonAllowlistedBeforeAnyDatabaseAccess is test
// requirement 2: a non-allowlisted caller must be rejected before any
// query runs. It proves this with a poison pill rather than just asserting
// the happy path is narrow: pool is nil, a *pgxpool.Pool that panics on
// first use (Query/Begin/QueryRow all dereference it). If ListFirms ever
// touched the database before checking the allowlist - even to resolve the
// caller into ZonaryOS's own users table - this test would panic (and so
// fail) instead of cleanly returning ErrNotPlatformAdmin.
func TestListFirms_RejectsNonAllowlistedBeforeAnyDatabaseAccess(t *testing.T) {
	allow := platformadmin.NewAllowlist([]string{"admin@zonaryos.com"})
	caller := identity.Identity{
		Subject: "not-an-admin-subject",
		Email:   "not-an-admin@example.com",
	}

	_, err := platformadmin.ListFirms(context.Background(), nil, allow, caller)
	if !errors.Is(err, platformadmin.ErrNotPlatformAdmin) {
		t.Fatalf("expected ErrNotPlatformAdmin, got: %v", err)
	}
}
