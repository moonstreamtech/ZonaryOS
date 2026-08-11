// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package search is the cross-entity half of Part 1's full-text search
// foundation: GET /api/firms/{firmID}/search?q=&types=... fans out a
// single query string across several entity packages' own List functions
// in parallel, each already filtering by its own search_tsv column
// (migrations/0024), and returns one combined, type-tagged result list.
//
// Deliberately a coordinating package that imports the entity packages
// it searches (internal/inventory, internal/crm, internal/hr), the exact
// same shape internal/reports already establishes for its own KPI
// dashboard (which imports internal/workflow and internal/accounting) -
// not a new coupling pattern. Each entity package stays completely
// unaware this package exists; adding a new searchable type here never
// requires touching internal/inventory/internal/crm/internal/hr
// themselves, only this package's own type-dispatch table.
package search

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
)

// ErrInvalidType means types named an entity this package doesn't search -
// the closed vocabulary below, not an open-ended list.
var ErrInvalidType = errors.New("invalid search type")

// AllTypes is the closed vocabulary a `types=` query param may name - the
// same "closed, hardcoded, never taken from request input as anything
// but a lookup key" discipline internal/reports.entityRegistry
// establishes for its own field allow-lists.
var AllTypes = []string{"products", "customers", "people", "suppliers"}

func validType(t string) bool {
	for _, known := range AllTypes {
		if known == t {
			return true
		}
	}
	return false
}

// maxHitsPerType bounds each type's contribution to one Search call's
// result - a full-text search is expected to return a short, relevant
// list, not a firm's entire catalog; this is a response-size backstop,
// not a pagination mechanism (there is no "next page" for cross-entity
// search - a caller wanting more should narrow their query or use the
// type-specific list endpoint's own `?q=` directly).
const maxHitsPerType = 20

// Hit is one cross-entity search result - a generic enough shape to
// represent a product, customer, person, or supplier without exposing
// each entity's own full response schema (a caller wanting the full
// record follows up with that entity's own GET-by-id endpoint).
type Hit struct {
	Type     string
	ID       uuid.UUID
	Title    string
	Subtitle string
}

// Search runs q against every requested type in parallel (one goroutine
// per type, results collected into one slice) - each entity's own List
// function already runs its own permission.IsMember check and its own
// search_tsv-backed query (see internal/inventory.ListProducts,
// internal/crm.ListCustomers, internal/hr.ListPeople,
// internal/inventory.ListSuppliers), so this function makes no
// authorization decision of its own beyond validating types. An empty q
// returns no hits without touching the database at all - cross-entity
// search has no "browse everything" mode, unlike each entity's own list
// endpoint.
//
// ciaudit:ignore-firmid-check: this function itself never queries the
// database - it only fans out to ListProducts/ListCustomers/ListPeople/
// ListSuppliers, each of which runs its own permission.IsMember check
// against firmID before touching any row.
func Search(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, q string, types []string) ([]Hit, error) {
	for _, t := range types {
		if !validType(t) {
			return nil, errors.Join(ErrInvalidType, errors.New(t))
		}
	}
	if q == "" || len(types) == 0 {
		return []Hit{}, nil
	}

	wanted := make(map[string]bool, len(types))
	for _, t := range types {
		wanted[t] = true
	}

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		hits []Hit
		errs []error
	)

	run := func(fn func() ([]Hit, error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := fn()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			hits = append(hits, h...)
		}()
	}

	if wanted["products"] {
		run(func() ([]Hit, error) { return searchProducts(ctx, pool, firmID, userID, q) })
	}
	if wanted["customers"] {
		run(func() ([]Hit, error) { return searchCustomers(ctx, pool, firmID, userID, q) })
	}
	if wanted["people"] {
		run(func() ([]Hit, error) { return searchPeople(ctx, pool, firmID, userID, q) })
	}
	if wanted["suppliers"] {
		run(func() ([]Hit, error) { return searchSuppliers(ctx, pool, firmID, userID, q) })
	}
	wg.Wait()

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	// Stable, deterministic ordering across calls (goroutine completion
	// order is not) - grouped by type (in AllTypes' own order), then by
	// title within a type, matching how the frontend renders "grouped by
	// entity type".
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Type != hits[j].Type {
			return typeRank(hits[i].Type) < typeRank(hits[j].Type)
		}
		return hits[i].Title < hits[j].Title
	})
	return hits, nil
}

func typeRank(t string) int {
	for i, known := range AllTypes {
		if known == t {
			return i
		}
	}
	return len(AllTypes)
}

func truncate(hits []Hit) []Hit {
	if len(hits) > maxHitsPerType {
		return hits[:maxHitsPerType]
	}
	return hits
}

// ciaudit:ignore-firmid-check: a thin adapter over inventory.ListProducts,
// which runs its own permission.IsMember check against firmID before
// touching any row - this function makes no authorization decision of
// its own.
func searchProducts(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, q string) ([]Hit, error) {
	products, err := inventory.ListProducts(ctx, pool, firmID, userID, inventory.ListProductsOptions{Search: q})
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(products))
	for _, p := range products {
		hits = append(hits, Hit{Type: "products", ID: p.ID, Title: p.Name, Subtitle: p.SKU})
	}
	return truncate(hits), nil
}

// ciaudit:ignore-firmid-check: a thin adapter over crm.ListCustomers,
// which runs its own permission.IsMember check against firmID before
// touching any row - this function makes no authorization decision of
// its own.
func searchCustomers(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, q string) ([]Hit, error) {
	customers, err := crm.ListCustomers(ctx, pool, firmID, userID, crm.ListCustomersOptions{Search: q})
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(customers))
	for _, c := range customers {
		subtitle := ""
		if c.Email != nil {
			subtitle = *c.Email
		}
		hits = append(hits, Hit{Type: "customers", ID: c.ID, Title: c.Name, Subtitle: subtitle})
	}
	return truncate(hits), nil
}

// ciaudit:ignore-firmid-check: a thin adapter over hr.ListPeople, which
// runs its own permission.IsMember check against firmID before touching
// any row - this function makes no authorization decision of its own.
func searchPeople(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, q string) ([]Hit, error) {
	people, err := hr.ListPeople(ctx, pool, firmID, userID, hr.ListPeopleOptions{Search: q})
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(people))
	for _, p := range people {
		subtitle := ""
		if p.Email != nil {
			subtitle = *p.Email
		}
		hits = append(hits, Hit{Type: "people", ID: p.ID, Title: p.FullName, Subtitle: subtitle})
	}
	return truncate(hits), nil
}

// ciaudit:ignore-firmid-check: a thin adapter over
// inventory.ListSuppliers, which runs its own permission.IsMember check
// against firmID before touching any row - this function makes no
// authorization decision of its own.
func searchSuppliers(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, q string) ([]Hit, error) {
	suppliers, err := inventory.ListSuppliers(ctx, pool, firmID, userID, inventory.ListSuppliersOptions{Search: q})
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(suppliers))
	for _, s := range suppliers {
		subtitle := ""
		if s.Email != nil {
			subtitle = *s.Email
		}
		hits = append(hits, Hit{Type: "suppliers", ID: s.ID, Title: s.Name, Subtitle: subtitle})
	}
	return truncate(hits), nil
}
