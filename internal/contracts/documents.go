// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package contracts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const contractDocumentAuditEntityType = "contract_document"

var documentTypes = map[string]bool{
	"original": true, "amendment": true, "annex": true, "correspondence": true,
}

// Document is one contract_documents row.
type Document struct {
	ID          uuid.UUID
	FirmID      uuid.UUID
	ContractID  uuid.UUID
	Name        string
	DocumentURL *string
	Type        string
	UploadedAt  time.Time
	UploadedBy  uuid.UUID
}

// CreateDocumentInput is CreateDocument's request shape.
type CreateDocumentInput struct {
	Name        string
	DocumentURL string
	Type        string
}

// CreateDocument attaches a new document to contractID within firmID.
// Owner-gated, same tier as CreateContract itself - a contract's document
// list is part of its own structural data, not a separate lower-trust
// resource.
func CreateDocument(ctx context.Context, pool *pgxpool.Pool, firmID, userID, contractID uuid.UUID, input CreateDocumentInput) (Document, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Document{}, fmt.Errorf("%w: name must not be empty", ErrInvalidDocument)
	}
	if !documentTypes[input.Type] {
		return Document{}, fmt.Errorf("%w: type %q is not one of original/amendment/annex/correspondence", ErrInvalidDocument, input.Type)
	}

	var d Document
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

		exists, err := ExistsTx(ctx, tx, firmID, contractID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrContractNotFound
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO contract_documents (firm_id, contract_id, name, document_url, type, uploaded_by)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, firm_id, contract_id, name, document_url, type, uploaded_at, uploaded_by
		`, firmID, contractID, name, optionalString(input.DocumentURL), input.Type, userID)
		var scanErr error
		d, scanErr = scanDocumentRow(row)
		if scanErr != nil {
			return fmt.Errorf("insert contract document: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, d.ID, contractDocumentAuditEntityType, createAuditAction, map[string]any{"name": name, "contractId": contractID})
	})
	if err != nil {
		return Document{}, err
	}
	return d, nil
}

// ListDocuments returns contractID's documents within firmID, newest
// first. Member-gated.
func ListDocuments(ctx context.Context, pool *pgxpool.Pool, firmID, userID, contractID uuid.UUID) ([]Document, error) {
	var documents []Document
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, contract_id, name, document_url, type, uploaded_at, uploaded_by
			FROM contract_documents WHERE firm_id = $1 AND contract_id = $2 ORDER BY uploaded_at DESC
		`, firmID, contractID)
		if err != nil {
			return fmt.Errorf("list contract documents: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDocumentRow(rows)
			if err != nil {
				return err
			}
			documents = append(documents, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return documents, nil
}

func scanDocumentRow(row rowScanner) (Document, error) {
	var d Document
	if err := row.Scan(&d.ID, &d.FirmID, &d.ContractID, &d.Name, &d.DocumentURL, &d.Type, &d.UploadedAt, &d.UploadedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, ErrDocumentNotFound
		}
		return Document{}, err
	}
	return d, nil
}
