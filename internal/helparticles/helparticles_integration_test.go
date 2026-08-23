// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package helparticles_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/helparticles"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests run against a real, migrated Postgres so migrations/0043's
// own seed data (help_articles) and its search_tsv trigger are exercised
// for real, same convention as every other package's integration tests.
// Skipped unless ZONARYOS_TEST_ADMIN_DATABASE_URL is set - help_articles
// is global/read-only, so the admin pool alone is enough (no RLS/app
// pool needed here, unlike a firm-scoped table).
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	if dsn == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL must be set to run helparticles integration tests")
	}
	if err := zdb.Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := zdb.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestSearch_FindsSeededArticleByEnglishKeyword is the batch's own
// required test case: "full-text search returns relevant articles."
func TestSearch_FindsSeededArticleByEnglishKeyword(t *testing.T) {
	pool := testPool(t)
	articles, err := helparticles.Search(context.Background(), pool, "invoice", "en")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, a := range articles {
		if a.Slug == "invoicing-basics" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected invoicing-basics in search results for %q, got %+v", "invoice", articles)
	}
}

func TestSearch_FindsSeededArticleByTurkishKeyword(t *testing.T) {
	pool := testPool(t)
	articles, err := helparticles.Search(context.Background(), pool, "fatura", "tr")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, a := range articles {
		if a.Slug == "invoicing-basics" {
			if a.Title != "Fatura oluşturma ve gönderme" {
				t.Fatalf("expected Turkish title for tr locale, got %q", a.Title)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected invoicing-basics in Turkish search results, got %+v", articles)
	}
}

func TestListForRoute_FiltersByRelatedRoute(t *testing.T) {
	pool := testPool(t)
	articles, err := helparticles.ListForRoute(context.Background(), pool, "/workflows", "en")
	if err != nil {
		t.Fatalf("ListForRoute: %v", err)
	}
	if len(articles) == 0 {
		t.Fatalf("expected at least one article for /workflows")
	}
	for _, a := range articles {
		if a.RelatedRoute == nil || *a.RelatedRoute != "/workflows" {
			t.Fatalf("expected only /workflows articles, got %+v", a)
		}
	}
}

func TestListForRoute_FallsBackToEnglishForUnsupportedLocale(t *testing.T) {
	pool := testPool(t)
	articles, err := helparticles.ListForRoute(context.Background(), pool, "/invoices", "ar")
	if err != nil {
		t.Fatalf("ListForRoute: %v", err)
	}
	if len(articles) == 0 {
		t.Fatalf("expected at least one article for /invoices")
	}
	if articles[0].Title != "Creating and sending invoices" {
		t.Fatalf("expected English fallback title for unsupported locale, got %q", articles[0].Title)
	}
}
