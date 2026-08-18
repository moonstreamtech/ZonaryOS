// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package integration is the data pipeline/ETL/system integrations
// batch: Part 1's connector/mapping data model, Part 2's outbound push
// (reusing internal/webhook's own delivery mechanics), Part 3's inbound
// pull scheduler, and Part 4's field-transform pipeline (transform.go).
//
// Scope boundaries (explicit, per this batch's own brief): only the
// http_webhook connector type actually executes (outbound push, inbound
// pull) in this batch - ftp/sftp/database/email_imap/custom are
// registered in the data model (migrations/0038's own CHECK constraint)
// but return ErrConnectorTypeNotSupported/ErrConnectionTestNotImplemented
// wherever real execution would otherwise happen, never a silent no-op.
// No OAuth flow (API key/URL config only). No conflict resolution for
// bidirectional sync - last-write-wins (see inbound.go's own doc
// comment). No real-time streaming - poll-based only (inbound.go).
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/cryptutil"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// ConnectorType is integration_connectors.type's closed vocabulary - a
// real DB CHECK constraint (migrations/0038).
type ConnectorType string

const (
	// #nosec G101 -- this is a connector type enum value (matching the DB
	// CHECK constraint's vocabulary), not a credential; gosec's "token"
	// heuristic false-positives on the "webhook" substring.
	ConnectorHTTPWebhook ConnectorType = "http_webhook"
	ConnectorFTP         ConnectorType = "ftp"
	ConnectorSFTP        ConnectorType = "sftp"
	ConnectorDatabase    ConnectorType = "database"
	ConnectorEmailIMAP   ConnectorType = "email_imap"
	ConnectorCustom      ConnectorType = "custom"
)

func validConnectorType(t ConnectorType) bool {
	switch t {
	case ConnectorHTTPWebhook, ConnectorFTP, ConnectorSFTP, ConnectorDatabase, ConnectorEmailIMAP, ConnectorCustom:
		return true
	default:
		return false
	}
}

// Connector is one integration_connectors row, with Config already
// MASKED (sensitive fields replaced by their own "...WXYZ" display
// form, see crypto.go's maskSensitiveConfigFields) - never the real
// encrypted-or-plaintext value. Internal callers that need the real
// config (outbound.go/inbound.go/TestConnection) use their own
// decryptedConfigTx instead of this struct's own Config field.
type Connector struct {
	ID             uuid.UUID
	FirmID         uuid.UUID
	Name           string
	Type           ConnectorType
	Config         map[string]any
	IsActive       bool
	LastSyncAt     *time.Time
	LastSyncStatus *string
	CreatedAt      time.Time
}

// ConnectorInput is CreateConnector/UpdateConnector's shared field shape
// - APIKey/Password/etc. sensitive fields go in Config just like any
// other field (keyed by the fixed sensitiveConfigKeys names,
// crypto.go); this input struct doesn't special-case them, encryption
// happens transparently inside CreateConnector/UpdateConnector.
type ConnectorInput struct {
	Name     string
	Type     ConnectorType
	Config   map[string]any
	IsActive bool
}

func validateConnectorInput(input ConnectorInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidConnector)
	}
	if !validConnectorType(input.Type) {
		return fmt.Errorf("%w: unknown type %q", ErrInvalidConnector, input.Type)
	}
	return nil
}

const connectorColumns = "id, firm_id, name, type, config, is_active, last_sync_at, last_sync_status, created_at"

func scanConnector(row pgx.Row) (Connector, error) {
	var c Connector
	var typeRaw string
	var configRaw []byte
	if err := row.Scan(&c.ID, &c.FirmID, &c.Name, &typeRaw, &configRaw, &c.IsActive, &c.LastSyncAt, &c.LastSyncStatus, &c.CreatedAt); err != nil {
		return Connector{}, err
	}
	c.Type = ConnectorType(typeRaw)
	if err := json.Unmarshal(configRaw, &c.Config); err != nil {
		return Connector{}, fmt.Errorf("unmarshal config: %w", err)
	}
	return c, nil
}

// CreateConnector adds a new integration connector for firmID - owner-
// gated: configuring where a firm's data gets pushed/pulled from is a
// structural firm decision, the same tier as internal/webhook.CreateWebhook.
func CreateConnector(ctx context.Context, pool *pgxpool.Pool, enc *cryptutil.Encryptor, firmID, userID uuid.UUID, input ConnectorInput) (Connector, error) {
	if err := validateConnectorInput(input); err != nil {
		return Connector{}, err
	}
	encryptedConfig, err := encryptSensitiveConfigFields(enc, input.Config)
	if err != nil {
		return Connector{}, err
	}
	configJSON, err := json.Marshal(encryptedConfig)
	if err != nil {
		return Connector{}, fmt.Errorf("marshal config: %w", err)
	}

	var connector Connector
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

		row := tx.QueryRow(ctx, `
			INSERT INTO integration_connectors (firm_id, name, type, config, is_active)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING `+connectorColumns,
			firmID, strings.TrimSpace(input.Name), string(input.Type), configJSON, input.IsActive,
		)
		c, err := scanConnector(row)
		if err != nil {
			return fmt.Errorf("insert connector: %w", err)
		}
		connector = c
		return nil
	})
	if err != nil {
		return Connector{}, err
	}
	connector.Config, err = maskSensitiveConfigFields(enc, connector.Config)
	if err != nil {
		return Connector{}, err
	}
	return connector, nil
}

// ListConnectors returns every connector for firmID, most recently
// created first - member-gated read (viewing what's configured doesn't
// need the stricter owner tier that changing it does, the same
// "member reads, owner writes" split internal/webhook.ListWebhooks/
// CreateWebhook already establish), with every Config masked.
func ListConnectors(ctx context.Context, pool *pgxpool.Pool, enc *cryptutil.Encryptor, firmID, userID uuid.UUID) ([]Connector, error) {
	var connectors []Connector
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+connectorColumns+` FROM integration_connectors WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list connectors: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanConnector(rows)
			if err != nil {
				return fmt.Errorf("scan connector: %w", err)
			}
			connectors = append(connectors, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	for i := range connectors {
		masked, err := maskSensitiveConfigFields(enc, connectors[i].Config)
		if err != nil {
			return nil, err
		}
		connectors[i].Config = masked
	}
	return connectors, nil
}

// UpdateConnector replaces connectorID's mutable fields - owner-gated,
// full-replace, same shape as internal/localization.UpdateTaxRate.
func UpdateConnector(ctx context.Context, pool *pgxpool.Pool, enc *cryptutil.Encryptor, firmID, userID, connectorID uuid.UUID, input ConnectorInput) (Connector, error) {
	if err := validateConnectorInput(input); err != nil {
		return Connector{}, err
	}
	encryptedConfig, err := encryptSensitiveConfigFields(enc, input.Config)
	if err != nil {
		return Connector{}, err
	}
	configJSON, err := json.Marshal(encryptedConfig)
	if err != nil {
		return Connector{}, fmt.Errorf("marshal config: %w", err)
	}

	var connector Connector
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

		row := tx.QueryRow(ctx, `
			UPDATE integration_connectors SET name = $1, type = $2, config = $3, is_active = $4
			WHERE id = $5 AND firm_id = $6
			RETURNING `+connectorColumns,
			strings.TrimSpace(input.Name), string(input.Type), configJSON, input.IsActive, connectorID, firmID,
		)
		c, err := scanConnector(row)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrConnectorNotFound
			}
			return fmt.Errorf("update connector: %w", err)
		}
		connector = c
		return nil
	})
	if err != nil {
		return Connector{}, err
	}
	connector.Config, err = maskSensitiveConfigFields(enc, connector.Config)
	if err != nil {
		return Connector{}, err
	}
	return connector, nil
}

// DeleteConnector removes connectorID - owner-gated, same tier as
// CreateConnector. Every integration_mappings row referencing it is
// removed too (ON DELETE CASCADE, migrations/0038).
func DeleteConnector(ctx context.Context, pool *pgxpool.Pool, firmID, userID, connectorID uuid.UUID) error {
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

		tag, err := tx.Exec(ctx, `DELETE FROM integration_connectors WHERE id = $1 AND firm_id = $2`, connectorID, firmID)
		if err != nil {
			return fmt.Errorf("delete connector: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrConnectorNotFound
		}
		return nil
	})
}

// getConnectorWithDecryptedConfigTx reads connectorID within an already-
// open transaction, with its config fully decrypted - internal use only
// (outbound.go/inbound.go/TestConnection), never returned over HTTP.
//
// ciaudit:ignore-firmid-check: internal helper - every caller
// (handleTestConnection, DispatchOutbound, ProcessInboundSyncs) either
// has already run its own permission check or is a system-triggered
// background read the same way internal/webhook.activeWebhooksForEvent's
// own doc comment justifies for its identical shape.
func getConnectorWithDecryptedConfigTx(ctx context.Context, tx pgx.Tx, enc *cryptutil.Encryptor, firmID, connectorID uuid.UUID) (Connector, error) {
	row := tx.QueryRow(ctx, `SELECT `+connectorColumns+` FROM integration_connectors WHERE id = $1 AND firm_id = $2`, connectorID, firmID)
	c, err := scanConnector(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Connector{}, ErrConnectorNotFound
		}
		return Connector{}, fmt.Errorf("get connector: %w", err)
	}
	c.Config, err = decryptSensitiveConfigFields(enc, c.Config)
	if err != nil {
		return Connector{}, err
	}
	return c, nil
}

// TestConnection checks connectorID is reachable - owner-gated, same
// tier as CreateConnector. For http_webhook: a plain GET ping against
// config["url"] (a HEAD would be more correct in principle, but many
// simple integration targets don't implement HEAD - GET is the more
// broadly compatible choice, matching this batch's own "keep it simple"
// scope). Every other connector type returns
// ErrConnectionTestNotImplemented - Part 1's own "others return 'not
// implemented' for now" contract, never a silently-successful no-op.
func TestConnection(ctx context.Context, pool *pgxpool.Pool, enc *cryptutil.Encryptor, firmID, userID, connectorID uuid.UUID) error {
	var connector Connector
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

		connector, err = getConnectorWithDecryptedConfigTx(ctx, tx, enc, firmID, connectorID)
		return err
	})
	if err != nil {
		return err
	}

	if connector.Type != ConnectorHTTPWebhook {
		return ErrConnectionTestNotImplemented
	}
	targetURL, _ := connector.Config["url"].(string)
	if strings.TrimSpace(targetURL) == "" {
		return fmt.Errorf("%w: connector config has no \"url\" field", ErrInvalidConnector)
	}
	return pingURL(ctx, targetURL)
}
