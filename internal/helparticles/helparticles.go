// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package helparticles serves the contextual help library: a small,
// global (not firm-scoped) set of Markdown articles seeded via migration
// (migrations/0043_help_articles_seed.up.sql) rather than any runtime
// authoring path - there is no POST/PATCH/DELETE endpoint here, on
// purpose. Help content is product documentation, reviewed and shipped
// the same way any other schema change is; letting a firm edit or add
// its own articles would turn this into a knowledge-base feature, well
// outside this batch's own "contextual help system" scope.
//
// Locale handling: title_en/content_en are always present; title_tr/
// content_tr are required too (the design brief's own seed-data
// requirement covers both shipped locales). A caller asking for a third
// locale (e.g. "ar", present in the frontend's own routing config but
// not yet a mandatory column here - see the migration's own doc comment)
// falls back to English rather than erroring, the same "never block on a
// missing translation" judgment call internal/localization's own
// formatting helpers make elsewhere in this codebase.
package helparticles

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Article is one help_articles row, resolved to a single locale.
type Article struct {
	ID           uuid.UUID
	Slug         string
	Title        string
	Content      string
	RelatedRoute *string
}

func resolveLocale(locale string) string {
	if strings.EqualFold(locale, "tr") {
		return "tr"
	}
	return "en"
}

// ListForRoute returns every article whose related_route matches route
// exactly - the contextual help panel's own "what applies to the page
// I'm on right now" query. An empty route returns every article (the
// panel's own "browse everything" fallback).
func ListForRoute(ctx context.Context, pool *pgxpool.Pool, route, locale string) ([]Article, error) {
	loc := resolveLocale(locale)
	var query string
	var args []any
	if strings.TrimSpace(route) == "" {
		query = articleSelect(loc) + ` ORDER BY title_en`
	} else {
		query = articleSelect(loc) + ` WHERE related_route = $1 ORDER BY title_en`
		args = append(args, route)
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list help articles: %w", err)
	}
	defer rows.Close()
	return scanArticles(rows)
}

// Search full-text searches title/content across both locales via
// search_tsv (migrations/0042's trigger-maintained column) - same
// plainto_tsquery('simple', normalize_search_text(...)) pattern
// migrations/0024_fulltext_search_filtering.up.sql establishes for every
// other searchable entity in this codebase.
func Search(ctx context.Context, pool *pgxpool.Pool, q, locale string) ([]Article, error) {
	loc := resolveLocale(locale)
	rows, err := pool.Query(ctx, articleSelect(loc)+`
		WHERE search_tsv @@ plainto_tsquery('simple', normalize_search_text($1))
		ORDER BY title_en
	`, q)
	if err != nil {
		return nil, fmt.Errorf("search help articles: %w", err)
	}
	defer rows.Close()
	return scanArticles(rows)
}

func articleSelect(locale string) string {
	titleCol, contentCol := "title_en", "content_en"
	if locale == "tr" {
		titleCol, contentCol = "title_tr", "content_tr"
	}
	return fmt.Sprintf(`SELECT id, slug, %s, %s, related_route FROM help_articles`, titleCol, contentCol)
}

func scanArticles(rows pgx.Rows) ([]Article, error) {
	var articles []Article
	for rows.Next() {
		var a Article
		if err := rows.Scan(&a.ID, &a.Slug, &a.Title, &a.Content, &a.RelatedRoute); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}
