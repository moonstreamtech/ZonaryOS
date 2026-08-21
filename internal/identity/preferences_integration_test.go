// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package identity_test

import (
	"context"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// TestPatchPreferences_Persists is the batch's own required test case:
// "PATCH persists" - user_preferences has no firm_id/RLS (see
// migrations/0042's own doc comment), so a plain app-role pool is enough,
// no WithFirmContext/firm fixture needed.
func TestPatchPreferences_Persists(t *testing.T) {
	adminDSN, appDSN := testPools(t)
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	if _, err := adminPool.Exec(ctx, `TRUNCATE users, user_preferences CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err := zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	defer appPool.Close()

	userID, err := identity.ResolveOrCreateUser(ctx, appPool, identity.Identity{
		Subject: "kc-prefs-1", Email: "prefs@example.com", DisplayName: "Prefs User",
	})
	if err != nil {
		t.Fatalf("resolve user: %v", err)
	}

	empty, err := identity.GetPreferences(ctx, appPool, userID)
	if err != nil {
		t.Fatalf("GetPreferences (before any patch): %v", err)
	}
	if empty.Theme != nil || empty.Density != nil || empty.DefaultLocale != nil {
		t.Fatalf("expected zero-value preferences before any patch, got %+v", empty)
	}

	theme, density := "dark", "compact"
	updated, err := identity.PatchPreferences(ctx, appPool, userID, identity.Preferences{Theme: &theme, Density: &density})
	if err != nil {
		t.Fatalf("PatchPreferences: %v", err)
	}
	if updated.Theme == nil || *updated.Theme != "dark" {
		t.Fatalf("expected theme=dark, got %+v", updated)
	}
	if updated.Density == nil || *updated.Density != "compact" {
		t.Fatalf("expected density=compact, got %+v", updated)
	}

	// A second, partial patch must leave the untouched field alone -
	// PatchPreferences' own partial-update contract.
	locale := "tr"
	final, err := identity.PatchPreferences(ctx, appPool, userID, identity.Preferences{DefaultLocale: &locale})
	if err != nil {
		t.Fatalf("second PatchPreferences: %v", err)
	}
	if final.Theme == nil || *final.Theme != "dark" {
		t.Fatalf("expected theme to remain dark after partial patch, got %+v", final)
	}
	if final.DefaultLocale == nil || *final.DefaultLocale != "tr" {
		t.Fatalf("expected defaultLocale=tr, got %+v", final)
	}

	// Re-reading from a fresh call proves it was actually persisted, not
	// just returned from the in-memory patch result.
	reread, err := identity.GetPreferences(ctx, appPool, userID)
	if err != nil {
		t.Fatalf("GetPreferences (after patch): %v", err)
	}
	if reread.Theme == nil || *reread.Theme != "dark" || reread.DefaultLocale == nil || *reread.DefaultLocale != "tr" {
		t.Fatalf("expected persisted preferences, got %+v", reread)
	}
}

func TestPatchPreferences_RejectsInvalidValue(t *testing.T) {
	adminDSN, appDSN := testPools(t)
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	if _, err := adminPool.Exec(ctx, `TRUNCATE users, user_preferences CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err := zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	defer appPool.Close()

	userID, err := identity.ResolveOrCreateUser(ctx, appPool, identity.Identity{
		Subject: "kc-prefs-2", Email: "prefs2@example.com", DisplayName: "Prefs User 2",
	})
	if err != nil {
		t.Fatalf("resolve user: %v", err)
	}

	bogus := "purple"
	if _, err := identity.PatchPreferences(ctx, appPool, userID, identity.Preferences{Theme: &bogus}); err == nil {
		t.Fatalf("expected an error for an invalid theme value")
	}
}
