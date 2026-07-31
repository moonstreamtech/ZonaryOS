// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package firm holds the narrow set of firm-level mutations that don't
// belong to any other module: renaming a firm, and (Open Points item 36)
// editing a small, concrete set of broader firm-metadata fields -
// address, tax_id, default_locale, default_currency, logo_url
// (migrations/0006_firm_metadata.up.sql). Still deliberately not a
// general "firm settings" package with its own extensible schema: item
// 36's remaining open questions (whether `firms.attributes` jsonb should
// eventually hold something more, and whether firm settings should
// become a real versioned module) are unresolved product questions this
// package does not attempt to answer - see docs/OPEN_POINTS.md item 36.
package firm

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// renameAuditAction is the audit_log.action Update records - reuses
// audit_log's existing shape (entity_type "firm", same table
// internal/wizard.CreateDefaultFirm's own firm-creation entry uses),
// distinguishing an update from the "create" action that table already
// carries for the same entity_type. Kept as one shared action for both a
// name-only update and a broader metadata update - the audit_log
// `changes` payload already records exactly which fields moved.
const renameAuditAction = "update"

// Item 36's basic garbage-input sanity bounds on the new free-text
// metadata fields - not fiscal-format validation (Open Points item 24
// stays open). No existing string column in this codebase has a length
// limit to match (firms.name itself never had one, see ErrInvalidName's
// doc comment), so these are new, deliberately generous bounds picked to
// reject obvious garbage (e.g. someone pasting a multi-KB blob into a
// text input) without constraining any real-world address/tax-ID/URL.
const (
	maxAddressLen  = 500
	maxTaxIDLen    = 100
	maxLocaleLen   = 20
	maxCurrencyLen = 10
	maxLogoURLLen  = 2048
)

// UpdateFields is Update's optional-metadata half (Open Points item 36):
// every field is a pointer so the caller can distinguish "leave this
// column alone" (nil) from "clear it back to NULL" (pointer to an empty
// string) from "set it to this value" (pointer to a non-empty string) -
// a PATCH that only wants to change, say, the logo URL must not
// accidentally blank out an address nobody meant to touch.
type UpdateFields struct {
	Address         *string
	TaxID           *string
	DefaultLocale   *string
	DefaultCurrency *string
	LogoURL         *string
}

// normalizeOptionalField trims s and enforces maxLen, returning the value
// to store (nil clears the column back to NULL, matching this migration's
// "unset" state) or ErrMetadataTooLong if it's still over-length after
// trimming.
func normalizeOptionalField(s string, maxLen int) (*string, error) {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) > maxLen {
		return nil, ErrMetadataTooLong
	}
	if trimmed == "" {
		return nil, nil
	}
	return &trimmed, nil
}

// UpdateName renames firmID - a thin wrapper over Update kept for
// existing callers/tests that only ever touch the name (this package's
// original, item-4 mutation): equivalent to calling Update with no
// UpdateFields set.
//
// ciaudit:ignore-firmid-check: pure delegation to Update, which performs
// the IsMember/IsOwner check itself before touching anything.
func UpdateName(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, name string) error {
	return Update(ctx, pool, firmID, userID, name, UpdateFields{})
}

// Update renames firmID to name and, per fields, updates any of the Open
// Points item 36 broader-metadata columns (migrations/0006_firm_metadata.up.sql) -
// all in one UPDATE/audit_log write. Owner-gated
// (internal/permission.IsOwner, after IsMember - same order every owner-
// gated function in this codebase checks in, e.g.
// internal/permission.GrantPermission): editing firm-level metadata is
// the same structural-decision tier as renaming the firm or defining a
// new workflow (internal/workflow.DefineWorkflowForFirm), not something
// every member can do.
func Update(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, name string, fields UpdateFields) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidName
	}

	var address, taxID, defaultLocale, defaultCurrency, logoURL *string
	var err error
	if fields.Address != nil {
		if address, err = normalizeOptionalField(*fields.Address, maxAddressLen); err != nil {
			return err
		}
	}
	if fields.TaxID != nil {
		if taxID, err = normalizeOptionalField(*fields.TaxID, maxTaxIDLen); err != nil {
			return err
		}
	}
	if fields.DefaultLocale != nil {
		if defaultLocale, err = normalizeOptionalField(*fields.DefaultLocale, maxLocaleLen); err != nil {
			return err
		}
	}
	if fields.DefaultCurrency != nil {
		if defaultCurrency, err = normalizeOptionalField(*fields.DefaultCurrency, maxCurrencyLen); err != nil {
			return err
		}
	}
	if fields.LogoURL != nil {
		if logoURL, err = normalizeOptionalField(*fields.LogoURL, maxLogoURLLen); err != nil {
			return err
		}
	}

	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrPermissionDenied
		}

		changes := map[string]any{"name": name}
		// COALESCE($n, column) leaves a column untouched when the
		// caller's UpdateFields didn't set that pointer at all (nil ->
		// SQL NULL parameter -> COALESCE falls back to the existing
		// value) - distinct from fields.X pointing at an empty string,
		// which normalizeOptionalField above already turned into a
		// literal nil *string meaning "set the column to NULL", which
		// this statement passes straight through instead of COALESCE-ing.
		// The two only look the same at the Go type level (both *string
		// nil) once normalizeOptionalField has run, so `set` below tracks
		// "was this field present in the request at all" independently.
		if _, err := tx.Exec(ctx, `
			UPDATE firms
			SET name = $1,
				address = CASE WHEN $2 THEN $3 ELSE address END,
				tax_id = CASE WHEN $4 THEN $5 ELSE tax_id END,
				default_locale = CASE WHEN $6 THEN $7 ELSE default_locale END,
				default_currency = CASE WHEN $8 THEN $9 ELSE default_currency END,
				logo_url = CASE WHEN $10 THEN $11 ELSE logo_url END
			WHERE id = $12
		`,
			name,
			fields.Address != nil, address,
			fields.TaxID != nil, taxID,
			fields.DefaultLocale != nil, defaultLocale,
			fields.DefaultCurrency != nil, defaultCurrency,
			fields.LogoURL != nil, logoURL,
			firmID,
		); err != nil {
			return err
		}

		if fields.Address != nil {
			changes["address"] = derefOrNull(address)
		}
		if fields.TaxID != nil {
			changes["taxId"] = derefOrNull(taxID)
		}
		if fields.DefaultLocale != nil {
			changes["defaultLocale"] = derefOrNull(defaultLocale)
		}
		if fields.DefaultCurrency != nil {
			changes["defaultCurrency"] = derefOrNull(defaultCurrency)
		}
		if fields.LogoURL != nil {
			changes["logoUrl"] = derefOrNull(logoURL)
		}

		return auditlog.Write(ctx, tx, firmID, userID, firmID, "firm", renameAuditAction, changes)
	})
}

// derefOrNull returns *s, or nil if s is nil - used only to build the
// audit_log `changes` payload above so a cleared field is recorded as
// JSON null rather than the Go zero value for string.
func derefOrNull(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// Metadata is one firm's editable fields as Get returns them - Name plus
// the Open Points item 36 broader-metadata columns. Every metadata field
// is nullable (nil means the firm never set it, matching the migration's
// no-backfill NULL default).
type Metadata struct {
	FirmID          uuid.UUID
	Name            string
	Address         *string
	TaxID           *string
	DefaultLocale   *string
	DefaultCurrency *string
	LogoURL         *string
}

// Get reads firmID's current name and broader metadata, gated by plain
// membership (internal/permission.IsMember) rather than IsOwner - viewing
// a firm's own basic info (what the settings page shows every member
// today, per FirmNameEditor's read-only fallback) doesn't need the
// stricter owner tier that editing it does; the settings page still
// decides client-side whether to render an editable form or a read-only
// value based on the caller's own IsOwner result from
// internal/identity.RoleInFirm.
func Get(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) (Metadata, error) {
	var m Metadata
	m.FirmID = firmID

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		return tx.QueryRow(ctx, `
			SELECT name, address, tax_id, default_locale, default_currency, logo_url
			FROM firms WHERE id = $1
		`, firmID).Scan(&m.Name, &m.Address, &m.TaxID, &m.DefaultLocale, &m.DefaultCurrency, &m.LogoURL)
	})
	if err != nil {
		return Metadata{}, err
	}
	return m, nil
}
