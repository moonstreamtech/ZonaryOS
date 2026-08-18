// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

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

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// SyncDirection is integration_mappings.sync_direction's closed
// vocabulary - a real DB CHECK constraint (migrations/0038).
type SyncDirection string

const (
	SyncInbound       SyncDirection = "inbound"
	SyncOutbound      SyncDirection = "outbound"
	SyncBidirectional SyncDirection = "bidirectional"
)

func validSyncDirection(d SyncDirection) bool {
	switch d {
	case SyncInbound, SyncOutbound, SyncBidirectional:
		return true
	default:
		return false
	}
}

// Mapping is one integration_mappings row.
type Mapping struct {
	ID            uuid.UUID
	FirmID        uuid.UUID
	ConnectorID   uuid.UUID
	SourceEntity  string
	TargetEntity  string
	FieldMappings []FieldMapping
	SyncDirection SyncDirection
	IsActive      bool
	CreatedAt     time.Time
}

// MappingInput is CreateMapping/UpdateMapping's shared field shape.
type MappingInput struct {
	ConnectorID   uuid.UUID
	SourceEntity  string
	TargetEntity  string
	FieldMappings []FieldMapping
	SyncDirection SyncDirection
	IsActive      bool
}

func validateMappingInput(input MappingInput) error {
	if strings.TrimSpace(input.SourceEntity) == "" {
		return fmt.Errorf("%w: sourceEntity must not be empty", ErrInvalidMapping)
	}
	if strings.TrimSpace(input.TargetEntity) == "" {
		return fmt.Errorf("%w: targetEntity must not be empty", ErrInvalidMapping)
	}
	if len(input.FieldMappings) == 0 {
		return fmt.Errorf("%w: fieldMappings must not be empty", ErrInvalidMapping)
	}
	for _, fm := range input.FieldMappings {
		if strings.TrimSpace(fm.SourceField) == "" || strings.TrimSpace(fm.TargetField) == "" {
			return fmt.Errorf("%w: every field mapping needs a non-empty sourceField and targetField", ErrInvalidMapping)
		}
		for _, t := range fm.Transform {
			if err := validateTransformSpec(t); err != nil {
				return fmt.Errorf("%w: field %q: %v", ErrInvalidMapping, fm.SourceField, err)
			}
		}
	}
	if !validSyncDirection(input.SyncDirection) {
		return fmt.Errorf("%w: unknown syncDirection %q", ErrInvalidMapping, input.SyncDirection)
	}
	return nil
}

const mappingColumns = "id, firm_id, connector_id, source_entity, target_entity, field_mappings, sync_direction, is_active, created_at"

func scanMapping(row pgx.Row) (Mapping, error) {
	var m Mapping
	var directionRaw string
	var fieldMappingsRaw []byte
	if err := row.Scan(&m.ID, &m.FirmID, &m.ConnectorID, &m.SourceEntity, &m.TargetEntity, &fieldMappingsRaw, &directionRaw, &m.IsActive, &m.CreatedAt); err != nil {
		return Mapping{}, err
	}
	m.SyncDirection = SyncDirection(directionRaw)
	if err := json.Unmarshal(fieldMappingsRaw, &m.FieldMappings); err != nil {
		return Mapping{}, fmt.Errorf("unmarshal field mappings: %w", err)
	}
	return m, nil
}

// CreateMapping adds a new field mapping under connectorID - owner-
// gated, same tier as CreateConnector.
func CreateMapping(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input MappingInput) (Mapping, error) {
	if err := validateMappingInput(input); err != nil {
		return Mapping{}, err
	}
	fieldMappingsJSON, err := json.Marshal(input.FieldMappings)
	if err != nil {
		return Mapping{}, fmt.Errorf("marshal field mappings: %w", err)
	}

	var mapping Mapping
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

		var connectorExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM integration_connectors WHERE id = $1 AND firm_id = $2)`, input.ConnectorID, firmID).Scan(&connectorExists); err != nil {
			return fmt.Errorf("check connector exists: %w", err)
		}
		if !connectorExists {
			return ErrConnectorNotFound
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO integration_mappings (firm_id, connector_id, source_entity, target_entity, field_mappings, sync_direction, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+mappingColumns,
			firmID, input.ConnectorID, strings.TrimSpace(input.SourceEntity), strings.TrimSpace(input.TargetEntity),
			fieldMappingsJSON, string(input.SyncDirection), input.IsActive,
		)
		m, err := scanMapping(row)
		if err != nil {
			return fmt.Errorf("insert mapping: %w", err)
		}
		mapping = m
		return nil
	})
	if err != nil {
		return Mapping{}, err
	}
	return mapping, nil
}

// ListMappings returns every mapping for firmID, optionally filtered to
// one connectorID (uuid.Nil means "every connector") - member-gated
// read, same tier as ListConnectors.
func ListMappings(ctx context.Context, pool *pgxpool.Pool, firmID, userID, connectorID uuid.UUID) ([]Mapping, error) {
	var mappings []Mapping
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		query := `SELECT ` + mappingColumns + ` FROM integration_mappings WHERE firm_id = $1`
		args := []any{firmID}
		if connectorID != uuid.Nil {
			query += ` AND connector_id = $2`
			args = append(args, connectorID)
		}
		query += ` ORDER BY created_at DESC`

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list mappings: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMapping(rows)
			if err != nil {
				return fmt.Errorf("scan mapping: %w", err)
			}
			mappings = append(mappings, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return mappings, nil
}

// UpdateMapping replaces mappingID's mutable fields - owner-gated,
// full-replace, same shape as UpdateConnector.
func UpdateMapping(ctx context.Context, pool *pgxpool.Pool, firmID, userID, mappingID uuid.UUID, input MappingInput) (Mapping, error) {
	if err := validateMappingInput(input); err != nil {
		return Mapping{}, err
	}
	fieldMappingsJSON, err := json.Marshal(input.FieldMappings)
	if err != nil {
		return Mapping{}, fmt.Errorf("marshal field mappings: %w", err)
	}

	var mapping Mapping
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
			UPDATE integration_mappings SET connector_id = $1, source_entity = $2, target_entity = $3,
				field_mappings = $4, sync_direction = $5, is_active = $6
			WHERE id = $7 AND firm_id = $8
			RETURNING `+mappingColumns,
			input.ConnectorID, strings.TrimSpace(input.SourceEntity), strings.TrimSpace(input.TargetEntity),
			fieldMappingsJSON, string(input.SyncDirection), input.IsActive, mappingID, firmID,
		)
		m, err := scanMapping(row)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrMappingNotFound
			}
			return fmt.Errorf("update mapping: %w", err)
		}
		mapping = m
		return nil
	})
	if err != nil {
		return Mapping{}, err
	}
	return mapping, nil
}

// DeleteMapping removes mappingID - owner-gated, same tier as
// CreateMapping.
func DeleteMapping(ctx context.Context, pool *pgxpool.Pool, firmID, userID, mappingID uuid.UUID) error {
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

		tag, err := tx.Exec(ctx, `DELETE FROM integration_mappings WHERE id = $1 AND firm_id = $2`, mappingID, firmID)
		if err != nil {
			return fmt.Errorf("delete mapping: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrMappingNotFound
		}
		return nil
	})
}
