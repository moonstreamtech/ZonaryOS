// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package inventory

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// outboundDispatch is the data pipeline/ETL/system integrations batch's
// own outbound-push hook - a package-level func var (default a no-op) so
// this package does NOT import internal/integration directly.
// internal/integration.DispatchOutbound itself needs to call
// CreateProduct/UpdateProduct (Part 3's own inbound "create/update by
// natural key" logic) - internal/inventory importing internal/integration
// back would complete an import cycle. Wired to the real implementation
// exactly once, at startup, via SetOutboundDispatcher
// (cmd/server/main.go: inventory.SetOutboundDispatcher(integration.DispatchOutbound)),
// the same "canonical function lives in the consumer, a matching-signature
// func var takes its real implementation at startup" shape
// internal/workflow/plugin_hooks.go's own RegisterTransitionHook
// establishes for its own import-cycle problem.
var outboundDispatch = func(pool *pgxpool.Pool, firmID uuid.UUID, sourceEntity string, payload map[string]any) {}

// SetOutboundDispatcher wires outboundDispatch - see that var's own doc
// comment.
func SetOutboundDispatcher(f func(pool *pgxpool.Pool, firmID uuid.UUID, sourceEntity string, payload map[string]any)) {
	outboundDispatch = f
}

// derefStrAny returns nil (not a typed nil *string) for a nil pointer,
// or the pointed-to string otherwise - outboundDispatch's own payload
// map is walked programmatically by internal/integration.ApplyFieldMappings
// BEFORE any JSON marshaling happens, so a raw *string value would reach
// its own toString type switch as an unrecognized type (falling through
// to a Go-syntax %v representation like "0xc0001234") - unlike
// webhook.Dispatch's payload, which goes straight to json.Marshal and
// so has no such problem with a *string value.
func derefStrAny(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
