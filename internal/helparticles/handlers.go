// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package helparticles

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// RegisterRoutes wires this package's two read-only endpoints into mux.
// Neither is firm-scoped (help content is global, see this package's own
// doc comment) - both still require a valid authenticated session
// (identity.Middleware), the same "any signed-in user, not necessarily a
// firm member" tier internal/identity's own /api/me routes use, rather
// than the /api/firms/{firmID}/... convention firm-scoped route groups
// follow.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/help", auth(http.HandlerFunc(handleList(pool))))
	mux.Handle("GET /api/help/search", auth(http.HandlerFunc(handleSearch(pool))))
}

type articleResponse struct {
	ID           string  `json:"id"`
	Slug         string  `json:"slug"`
	Title        string  `json:"title"`
	Content      string  `json:"content"`
	RelatedRoute *string `json:"relatedRoute,omitempty"`
}

func toArticleResponse(a Article) articleResponse {
	return articleResponse{ID: a.ID.String(), Slug: a.Slug, Title: a.Title, Content: a.Content, RelatedRoute: a.RelatedRoute}
}

func writeArticles(w http.ResponseWriter, articles []Article) {
	resp := make([]articleResponse, 0, len(articles))
	for _, a := range articles {
		resp = append(resp, toArticleResponse(a))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleList implements GET /api/help?route=&locale= - the contextual
// help panel's own "what applies to the page I'm on" query. An omitted
// route returns every article (see ListForRoute's own doc comment).
func handleList(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, present := identity.FromContext(r.Context()); !present {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}

		articles, err := ListForRoute(r.Context(), pool, r.URL.Query().Get("route"), r.URL.Query().Get("locale"))
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeArticles(w, articles)
	}
}

// handleSearch implements GET /api/help/search?q=&locale=.
func handleSearch(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, present := identity.FromContext(r.Context()); !present {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}

		q := r.URL.Query().Get("q")
		if q == "" {
			writeArticles(w, nil)
			return
		}

		articles, err := Search(r.Context(), pool, q, r.URL.Query().Get("locale"))
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeArticles(w, articles)
	}
}
