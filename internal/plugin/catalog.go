// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Plugin/extension architecture batch, Part 1: the plugins/
// firm_plugin_configs data model (migrations/0037) - a platform-wide
// catalog row per compiled-in plugin, plus per-firm opt-in/config. This
// is discoverability/enablement metadata ONLY: nothing in this file
// loads, unloads, or otherwise controls whether a plugin's own Go code
// actually runs - that is entirely Registry's own compile-time
// registration (registry.go, cmd/server/main.go). A plugin whose
// catalog row has is_enabled=false but was still registered via
// RegisterWorkflowHook/RegisterKPIProvider/RegisterReportSource still
// dispatches exactly as if it were enabled - Part 2's own "no dynamic
// loading" scope boundary means this batch does not wire the catalog's
// own is_enabled flag into the dispatch path at all. A future batch that
// wants the catalog to actually gate dispatch would need to check
// IsPluginEnabledForFirm (or similar) inside DispatchTransition/
// GetKPIs/Query themselves - deliberately not built here.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// CatalogEntry is one plugins row.
type CatalogEntry struct {
	ID           uuid.UUID
	Name         string
	Version      string
	Description  *string
	Author       *string
	HomepageURL  *string
	EntryPoint   string
	Capabilities []string
	IsEnabled    bool
	ConfigSchema map[string]any
	CreatedAt    time.Time
}

// CatalogEntryInput is CreatePlugin/UpdatePlugin's shared field shape.
type CatalogEntryInput struct {
	Name         string
	Version      string
	Description  string
	Author       string
	HomepageURL  string
	EntryPoint   string
	Capabilities []string
	IsEnabled    bool
	ConfigSchema map[string]any
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func validateCatalogEntryInput(input CatalogEntryInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidPlugin)
	}
	if strings.TrimSpace(input.Version) == "" {
		return fmt.Errorf("%w: version must not be empty", ErrInvalidPlugin)
	}
	if strings.TrimSpace(input.EntryPoint) == "" {
		return fmt.Errorf("%w: entryPoint must not be empty", ErrInvalidPlugin)
	}
	return nil
}

const catalogColumns = "id, name, version, description, author, homepage_url, entry_point, capabilities, is_enabled, config_schema, created_at"

func scanCatalogEntry(row pgx.Row) (CatalogEntry, error) {
	var e CatalogEntry
	var configSchemaRaw []byte
	if err := row.Scan(&e.ID, &e.Name, &e.Version, &e.Description, &e.Author, &e.HomepageURL,
		&e.EntryPoint, &e.Capabilities, &e.IsEnabled, &configSchemaRaw, &e.CreatedAt); err != nil {
		return CatalogEntry{}, err
	}
	if err := json.Unmarshal(configSchemaRaw, &e.ConfigSchema); err != nil {
		return CatalogEntry{}, fmt.Errorf("unmarshal config schema: %w", err)
	}
	return e, nil
}

// CreatePlugin adds a new plugins catalog row - platform-admin-gated at
// the HTTP layer (handlers.go), not here, the same division of
// responsibility internal/currency.CreateRate's own doc comment
// describes for its own platform-admin gate.
func CreatePlugin(ctx context.Context, pool *pgxpool.Pool, input CatalogEntryInput) (CatalogEntry, error) {
	if err := validateCatalogEntryInput(input); err != nil {
		return CatalogEntry{}, err
	}
	if input.ConfigSchema == nil {
		input.ConfigSchema = map[string]any{}
	}
	configSchemaJSON, err := json.Marshal(input.ConfigSchema)
	if err != nil {
		return CatalogEntry{}, fmt.Errorf("marshal config schema: %w", err)
	}

	row := pool.QueryRow(ctx, `
		INSERT INTO plugins (name, version, description, author, homepage_url, entry_point, capabilities, is_enabled, config_schema)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+catalogColumns,
		strings.TrimSpace(input.Name), strings.TrimSpace(input.Version), optionalString(input.Description),
		optionalString(input.Author), optionalString(input.HomepageURL), strings.TrimSpace(input.EntryPoint),
		input.Capabilities, input.IsEnabled, configSchemaJSON,
	)
	e, err := scanCatalogEntry(row)
	if err != nil {
		return CatalogEntry{}, fmt.Errorf("insert plugin: %w", err)
	}
	return e, nil
}

// ListPlugins returns every plugins catalog row, most recently created
// first - unauthenticated, same posture as
// internal/currency.ListRates/internal/localization.ListFiscalCountryConfigs:
// a plugin's own name/version/description/capabilities carry no
// sensitive content, and a firm's own frontend needs to browse the
// catalog before it has any firm-scoped bearer token specifically for
// this.
func ListPlugins(ctx context.Context, pool *pgxpool.Pool) ([]CatalogEntry, error) {
	rows, err := pool.Query(ctx, `SELECT `+catalogColumns+` FROM plugins ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list plugins: %w", err)
	}
	defer rows.Close()
	var entries []CatalogEntry
	for rows.Next() {
		e, err := scanCatalogEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan plugin: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// UpdatePlugin replaces pluginID's mutable fields - platform-admin-gated,
// full-replace, same shape as internal/localization's
// UpsertFiscalCountryConfig.
func UpdatePlugin(ctx context.Context, pool *pgxpool.Pool, pluginID uuid.UUID, input CatalogEntryInput) (CatalogEntry, error) {
	if err := validateCatalogEntryInput(input); err != nil {
		return CatalogEntry{}, err
	}
	if input.ConfigSchema == nil {
		input.ConfigSchema = map[string]any{}
	}
	configSchemaJSON, err := json.Marshal(input.ConfigSchema)
	if err != nil {
		return CatalogEntry{}, fmt.Errorf("marshal config schema: %w", err)
	}

	row := pool.QueryRow(ctx, `
		UPDATE plugins SET name = $1, version = $2, description = $3, author = $4, homepage_url = $5,
			entry_point = $6, capabilities = $7, is_enabled = $8, config_schema = $9
		WHERE id = $10
		RETURNING `+catalogColumns,
		strings.TrimSpace(input.Name), strings.TrimSpace(input.Version), optionalString(input.Description),
		optionalString(input.Author), optionalString(input.HomepageURL), strings.TrimSpace(input.EntryPoint),
		input.Capabilities, input.IsEnabled, configSchemaJSON, pluginID,
	)
	e, err := scanCatalogEntry(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return CatalogEntry{}, ErrPluginNotFound
		}
		return CatalogEntry{}, fmt.Errorf("update plugin: %w", err)
	}
	return e, nil
}

// DeletePlugin removes pluginID from the catalog - platform-admin-gated,
// same tier as CreatePlugin/UpdatePlugin. Every firm_plugin_configs row
// referencing it is removed too (ON DELETE CASCADE, migrations/0037).
func DeletePlugin(ctx context.Context, pool *pgxpool.Pool, pluginID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM plugins WHERE id = $1`, pluginID)
	if err != nil {
		return fmt.Errorf("delete plugin: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPluginNotFound
	}
	return nil
}

// FirmConfig is one firm_plugin_configs row.
type FirmConfig struct {
	ID        uuid.UUID
	FirmID    uuid.UUID
	PluginID  uuid.UUID
	Config    map[string]any
	IsEnabled bool
	CreatedAt time.Time
}

// FirmConfigInput is UpsertFirmPluginConfig's request shape.
type FirmConfigInput struct {
	PluginID  uuid.UUID
	Config    map[string]any
	IsEnabled bool
}

const firmConfigColumns = "id, firm_id, plugin_id, config, is_enabled, created_at"

func scanFirmConfig(row pgx.Row) (FirmConfig, error) {
	var c FirmConfig
	var configRaw []byte
	if err := row.Scan(&c.ID, &c.FirmID, &c.PluginID, &configRaw, &c.IsEnabled, &c.CreatedAt); err != nil {
		return FirmConfig{}, err
	}
	if err := json.Unmarshal(configRaw, &c.Config); err != nil {
		return FirmConfig{}, fmt.Errorf("unmarshal firm plugin config: %w", err)
	}
	return c, nil
}

// UpsertFirmPluginConfig enables (or reconfigures) pluginID for firmID -
// owner-gated: opting a firm into a plugin, and whatever config it's
// handed, is the same structural-decision tier as
// internal/localization.CreateTaxRate. plugin_id/firm_id is UNIQUE
// (migrations/0037), so this is a plain upsert (ON CONFLICT DO UPDATE),
// the same "one row per (firm, plugin) pair, edited in place" shape
// internal/localization.UpsertFiscalCountryConfig already establishes
// for its own (single-column) primary key.
func UpsertFirmPluginConfig(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input FirmConfigInput) (FirmConfig, error) {
	if input.Config == nil {
		input.Config = map[string]any{}
	}
	configJSON, err := json.Marshal(input.Config)
	if err != nil {
		return FirmConfig{}, fmt.Errorf("marshal firm plugin config: %w", err)
	}

	var cfg FirmConfig
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
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
			return ErrNotOwner
		}

		var pluginExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM plugins WHERE id = $1)`, input.PluginID).Scan(&pluginExists); err != nil {
			return fmt.Errorf("check plugin exists: %w", err)
		}
		if !pluginExists {
			return ErrPluginNotFound
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO firm_plugin_configs (firm_id, plugin_id, config, is_enabled)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (firm_id, plugin_id) DO UPDATE SET config = EXCLUDED.config, is_enabled = EXCLUDED.is_enabled
			RETURNING `+firmConfigColumns, firmID, input.PluginID, configJSON, input.IsEnabled)
		c, err := scanFirmConfig(row)
		if err != nil {
			return fmt.Errorf("upsert firm plugin config: %w", err)
		}
		cfg = c
		return nil
	})
	if err != nil {
		return FirmConfig{}, err
	}
	return cfg, nil
}

// ListFirmPluginConfigs returns every plugin firmID has configured -
// member-gated read, same tier as
// internal/localization.ListTaxRates/ListAddresses.
func ListFirmPluginConfigs(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]FirmConfig, error) {
	var configs []FirmConfig
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+firmConfigColumns+` FROM firm_plugin_configs WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list firm plugin configs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanFirmConfig(rows)
			if err != nil {
				return fmt.Errorf("scan firm plugin config: %w", err)
			}
			configs = append(configs, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return configs, nil
}

// DeleteFirmPluginConfig removes firmID's own configuration for pluginID
// (reverting to "not configured," not merely "disabled") - owner-gated,
// same tier as UpsertFirmPluginConfig.
func DeleteFirmPluginConfig(ctx context.Context, pool *pgxpool.Pool, firmID, userID, pluginID uuid.UUID) error {
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
			return ErrNotOwner
		}

		tag, err := tx.Exec(ctx, `DELETE FROM firm_plugin_configs WHERE firm_id = $1 AND plugin_id = $2`, firmID, pluginID)
		if err != nil {
			return fmt.Errorf("delete firm plugin config: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrFirmPluginConfigNotFound
		}
		return nil
	})
}
