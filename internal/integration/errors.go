// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package integration

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - connector/mapping writes are
	// owner-gated, the same tier as internal/localization.CreateTaxRate.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's integrations")

	// ErrConnectorNotFound means no integration_connectors row with the
	// given id is visible in the caller's firm context.
	ErrConnectorNotFound = errors.New("integration connector not found")

	// ErrInvalidConnector means CreateConnector/UpdateConnector was given
	// a structurally invalid connector (empty name, unknown type).
	ErrInvalidConnector = errors.New("invalid integration connector")

	// ErrMappingNotFound means no integration_mappings row with the given
	// id is visible in the caller's firm context.
	ErrMappingNotFound = errors.New("integration mapping not found")

	// ErrInvalidMapping means CreateMapping/UpdateMapping was given a
	// structurally invalid mapping (empty source/target entity, empty
	// field_mappings, unknown sync_direction).
	ErrInvalidMapping = errors.New("invalid integration mapping")

	// ErrEncryptionKeyNotSet means ZONARYOS_INTEGRATION_ENCRYPTION_KEY is
	// unset (or malformed) at the point a connector config containing a
	// sensitive field needs to be encrypted or decrypted - a real 500,
	// not something callers can route around. A connector whose config
	// has NO sensitive field (see crypto.go's own sensitiveConfigKeys)
	// never triggers this - only a config that actually needs encryption
	// does.
	ErrEncryptionKeyNotSet = errors.New("integration encryption key is not configured (ZONARYOS_INTEGRATION_ENCRYPTION_KEY)")

	// ErrConnectionTestNotImplemented means TestConnection was called
	// against a connector type other than http_webhook - Part 1's own
	// "others return 'not implemented' for now" contract.
	ErrConnectionTestNotImplemented = errors.New("connection test is not implemented for this connector type")

	// ErrConnectorTypeNotSupported means the inbound-sync scheduler
	// (inbound.go) reached a connector whose type is registered in the
	// data model but has no real execution in this batch (ftp/sftp/
	// database/email_imap/custom) - Part 3's own "no silent failures"
	// contract: this is written to integration_sync_logs.error_log, never
	// just skipped without a trace.
	ErrConnectorTypeNotSupported = errors.New("connector type not yet supported for sync execution")

	// ErrTargetEntityNotSupported means an inbound mapping's target_entity
	// isn't one of the entities this batch actually knows how to create/
	// update ("products", "customers") - same "no silent failures"
	// contract as ErrConnectorTypeNotSupported.
	ErrTargetEntityNotSupported = errors.New("target entity not yet supported for inbound sync")

	// ErrInvalidTransform means a field_mappings transform spec doesn't
	// name a known transform (transform.go), or names one with a
	// malformed argument (e.g. "multiply:not-a-number").
	ErrInvalidTransform = errors.New("invalid field transform")
)
