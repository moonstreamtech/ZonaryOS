// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const configAuditEntityType = "ai_configuration"

const (
	createConfigAuditAction = "create"
	deleteConfigAuditAction = "delete"
)

// Configuration is one ai_configurations row, as returned to a caller -
// APIKeyMasked, never the plaintext or even the ciphertext (see
// MaskAPIKey in crypto.go).
type Configuration struct {
	ID           uuid.UUID
	FirmID       uuid.UUID
	Provider     ProviderName
	Model        string
	APIKeyMasked string
	Settings     map[string]any
	IsActive     bool
}

func validProvider(p ProviderName) bool {
	switch p {
	case ProviderAnthropic, ProviderOpenAI, ProviderGoogle, ProviderCustom:
		return true
	default:
		return false
	}
}

// CreateConfigInput is CreateConfig's request shape - APIKey is the
// PLAINTEXT provider key, encrypted before it ever reaches the database
// (see Encryptor.Encrypt) and discarded from memory as soon as this call
// returns.
type CreateConfigInput struct {
	Provider ProviderName
	Model    string
	APIKey   string
}

// CreateConfig stores a new AI configuration for firmID, encrypting
// APIKey with encryptor. Owner-gated: configuring which external AI
// provider a firm's business data prompts get sent to is a structural
// firm decision, the same tier as internal/edgeagent.CreateAgent.
//
// The new configuration is created active, and every OTHER active
// configuration for the firm is deactivated in the same transaction
// (deactivateOtherActiveConfigsTx) - "only one active AI configuration
// per firm at a time," the same application-level "flip the others"
// pattern internal/budget's deactivateOtherActiveBudgetsTx and
// internal/manufacturing's deactivateOtherActiveBOMsTx already establish,
// not a DB constraint.
func CreateConfig(ctx context.Context, pool *pgxpool.Pool, encryptor *Encryptor, firmID, userID uuid.UUID, input CreateConfigInput) (Configuration, error) {
	if encryptor == nil {
		return Configuration{}, ErrEncryptionKeyNotSet
	}
	if !validProvider(input.Provider) {
		return Configuration{}, fmt.Errorf("%w: unknown provider %q", ErrInvalidConfig, input.Provider)
	}
	model := strings.TrimSpace(input.Model)
	if model == "" {
		return Configuration{}, fmt.Errorf("%w: model must not be empty", ErrInvalidConfig)
	}
	if strings.TrimSpace(input.APIKey) == "" {
		return Configuration{}, fmt.Errorf("%w: apiKey must not be empty", ErrInvalidConfig)
	}

	encrypted, err := encryptor.Encrypt(input.APIKey)
	if err != nil {
		return Configuration{}, fmt.Errorf("encrypt API key: %w", err)
	}

	var cfg Configuration
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

		cfg = Configuration{
			FirmID: firmID, Provider: input.Provider, Model: model,
			APIKeyMasked: MaskAPIKey(input.APIKey), Settings: map[string]any{}, IsActive: true,
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO ai_configurations (firm_id, provider, model, api_key_encrypted, is_active)
			VALUES ($1, $2, $3, $4, true)
			RETURNING id
		`, firmID, string(input.Provider), model, encrypted).Scan(&cfg.ID); err != nil {
			return fmt.Errorf("insert AI configuration: %w", err)
		}

		if err := deactivateOtherActiveConfigsTx(ctx, tx, firmID, cfg.ID); err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, cfg.ID, configAuditEntityType, createConfigAuditAction, map[string]any{
			"provider": string(input.Provider),
			"model":    model,
		})
	})
	if err != nil {
		return Configuration{}, err
	}
	return cfg, nil
}

// deactivateOtherActiveConfigsTx flips every OTHER ai_configurations row
// for firmID with is_active=true back to false - see CreateConfig's own
// doc comment for the "flip the others in the same transaction" pattern
// this mirrors.
//
// ciaudit:ignore-firmid-check: internal helper, only called from
// CreateConfig, which has already run its own permission.IsOwner check
// before calling this.
func deactivateOtherActiveConfigsTx(ctx context.Context, tx pgx.Tx, firmID, excludeConfigID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE ai_configurations SET is_active = false
		WHERE firm_id = $1 AND id != $2 AND is_active = true
	`, firmID, excludeConfigID); err != nil {
		return fmt.Errorf("deactivate other active AI configurations: %w", err)
	}
	return nil
}

const configColumns = "id, firm_id, provider, model, api_key_encrypted, settings, is_active"

func scanConfig(row pgx.Row, encryptor *Encryptor) (Configuration, error) {
	var c Configuration
	var providerRaw, encryptedKey string
	var settingsRaw []byte
	if err := row.Scan(&c.ID, &c.FirmID, &providerRaw, &c.Model, &encryptedKey, &settingsRaw, &c.IsActive); err != nil {
		return Configuration{}, err
	}
	c.Provider = ProviderName(providerRaw)
	if err := json.Unmarshal(settingsRaw, &c.Settings); err != nil {
		return Configuration{}, fmt.Errorf("unmarshal settings: %w", err)
	}

	if encryptor == nil {
		return Configuration{}, ErrEncryptionKeyNotSet
	}
	plaintext, err := encryptor.Decrypt(encryptedKey)
	if err != nil {
		return Configuration{}, fmt.Errorf("decrypt API key: %w", err)
	}
	c.APIKeyMasked = MaskAPIKey(plaintext)
	return c, nil
}

// ListConfigs lists every AI configuration for firmID, most recently
// created first. Owner-gated read, same tier as CreateConfig - a
// configuration record implies a stored provider API key, not something
// an ordinary member should be able to enumerate.
func ListConfigs(ctx context.Context, pool *pgxpool.Pool, encryptor *Encryptor, firmID, userID uuid.UUID) ([]Configuration, error) {
	var configs []Configuration
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
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

		rows, err := tx.Query(ctx, `SELECT `+configColumns+` FROM ai_configurations WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list AI configurations: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			c, err := scanConfig(rows, encryptor)
			if err != nil {
				return fmt.Errorf("scan AI configuration: %w", err)
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

// DeleteConfig removes one AI configuration by id within firmID.
// Owner-gated, same tier as CreateConfig.
func DeleteConfig(ctx context.Context, pool *pgxpool.Pool, firmID, userID, configID uuid.UUID) error {
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

		tag, err := tx.Exec(ctx, `DELETE FROM ai_configurations WHERE id = $1 AND firm_id = $2`, configID, firmID)
		if err != nil {
			return fmt.Errorf("delete AI configuration: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrConfigNotFound
		}

		return auditlog.Write(ctx, tx, firmID, userID, configID, configAuditEntityType, deleteConfigAuditAction, map[string]any{})
	})
}

// getActiveConfigTx fetches the firm's single active AI configuration
// (decrypted server-side) within an already-open transaction, for
// internal use by suggest_workflow.go/suggest_report.go/anomaly.go - it
// does NOT run any permission check itself (callers that reach it have
// already done their own IsMember/IsOwner check, see handlers.go), and
// unlike scanConfig/ListConfigs it returns the PLAINTEXT key (never
// exposed past this package) because the caller needs it to actually
// call the provider.
//
// Returns ErrNoActiveConfig - the Part 5 opt-in sentinel - if the firm has
// no active configuration, which every /ai/* handler maps to 404.
//
// ciaudit:ignore-firmid-check: internal helper with no authorization of
// its own by design - SuggestWorkflow/SuggestReport call it only after
// their own IsMember/IsOwner check, and the anomaly scheduler
// (processAnomalyCheckForFirm) calls it with a firmID drawn from
// listAllFirmIDs, never from an HTTP caller.
func getActiveConfigTx(ctx context.Context, tx pgx.Tx, encryptor *Encryptor, firmID uuid.UUID) (Configuration, string, error) {
	var c Configuration
	var providerRaw, encryptedKey string
	var settingsRaw []byte
	err := tx.QueryRow(ctx, `
		SELECT id, firm_id, provider, model, api_key_encrypted, settings, is_active
		FROM ai_configurations WHERE firm_id = $1 AND is_active = true
		LIMIT 1
	`, firmID).Scan(&c.ID, &c.FirmID, &providerRaw, &c.Model, &encryptedKey, &settingsRaw, &c.IsActive)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Configuration{}, "", ErrNoActiveConfig
		}
		return Configuration{}, "", fmt.Errorf("get active AI configuration: %w", err)
	}
	c.Provider = ProviderName(providerRaw)
	if err := json.Unmarshal(settingsRaw, &c.Settings); err != nil {
		return Configuration{}, "", fmt.Errorf("unmarshal settings: %w", err)
	}

	if encryptor == nil {
		return Configuration{}, "", ErrEncryptionKeyNotSet
	}
	plaintext, err := encryptor.Decrypt(encryptedKey)
	if err != nil {
		return Configuration{}, "", fmt.Errorf("decrypt API key: %w", err)
	}
	c.APIKeyMasked = MaskAPIKey(plaintext)
	return c, plaintext, nil
}
