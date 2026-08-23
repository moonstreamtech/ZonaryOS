// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package identity

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Preferences is user_preferences.preferences, decoded. Every field is
// optional (a fresh user_preferences row defaults to '{}'::jsonb) - the
// frontend applies its own defaults ('system' theme, 'default' density,
// browser/Accept-Language locale) for whatever's unset, the same "server
// stores only what's been explicitly chosen" judgment call this
// codebase's other settings-style tables (e.g. firms.default_locale)
// already make.
type Preferences struct {
	Theme         *string `json:"theme,omitempty"`
	Density       *string `json:"density,omitempty"`
	DefaultLocale *string `json:"defaultLocale,omitempty"`
}

var validThemes = map[string]bool{"light": true, "dark": true, "system": true}
var validDensities = map[string]bool{"compact": true, "default": true, "comfortable": true}
var validLocales = map[string]bool{"en": true, "tr": true, "ar": true}

// ErrInvalidPreferences means PatchPreferences was given a theme/density/
// defaultLocale value outside the fixed enum the frontend actually
// understands.
var ErrInvalidPreferences = fmt.Errorf("invalid preferences")

func (p Preferences) validate() error {
	if p.Theme != nil && !validThemes[*p.Theme] {
		return fmt.Errorf("%w: theme must be one of light, dark, system", ErrInvalidPreferences)
	}
	if p.Density != nil && !validDensities[*p.Density] {
		return fmt.Errorf("%w: density must be one of compact, default, comfortable", ErrInvalidPreferences)
	}
	if p.DefaultLocale != nil && !validLocales[*p.DefaultLocale] {
		return fmt.Errorf("%w: defaultLocale must be one of en, tr, ar", ErrInvalidPreferences)
	}
	return nil
}

// GetPreferences returns userID's preferences, or a zero Preferences{} if
// userID has never set any - not an error, the same "no progress yet
// isn't an error condition" judgment call internal/onboarding.GetProgress
// makes. No firm context at all: user_preferences has no firm_id column
// and no RLS policy (see migrations/0042's own doc comment), the same
// global-identity tier as the `users` table itself - scoped purely by
// `WHERE user_id = $1` against the plain (non-RLS) pool, never
// db.WithFirmContext/WithUserContext, since there is no RLS policy here
// to satisfy.
func GetPreferences(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (Preferences, error) {
	var raw []byte
	err := pool.QueryRow(ctx, `SELECT preferences FROM user_preferences WHERE user_id = $1`, userID).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Preferences{}, nil
		}
		return Preferences{}, fmt.Errorf("look up preferences: %w", err)
	}
	var p Preferences
	if err := json.Unmarshal(raw, &p); err != nil {
		return Preferences{}, fmt.Errorf("unmarshal preferences: %w", err)
	}
	return p, nil
}

// PatchPreferences merges patch's non-nil fields into userID's stored
// preferences (a partial update - same "nil leaves that field unchanged"
// convention as e.g. internal/inventory.ProductUpdate) and returns the
// resulting full Preferences.
func PatchPreferences(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, patch Preferences) (Preferences, error) {
	if err := patch.validate(); err != nil {
		return Preferences{}, err
	}

	current, err := GetPreferences(ctx, pool, userID)
	if err != nil {
		return Preferences{}, err
	}
	if patch.Theme != nil {
		current.Theme = patch.Theme
	}
	if patch.Density != nil {
		current.Density = patch.Density
	}
	if patch.DefaultLocale != nil {
		current.DefaultLocale = patch.DefaultLocale
	}

	encoded, err := json.Marshal(current)
	if err != nil {
		return Preferences{}, fmt.Errorf("marshal preferences: %w", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO user_preferences (user_id, preferences, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET preferences = $2, updated_at = now()
	`, userID, encoded); err != nil {
		return Preferences{}, fmt.Errorf("upsert preferences: %w", err)
	}

	return current, nil
}
