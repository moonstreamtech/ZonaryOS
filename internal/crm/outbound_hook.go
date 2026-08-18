// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package crm

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// outboundDispatch is the data pipeline/ETL/system integrations batch's
// own outbound-push hook - see internal/inventory/outbound_hook.go's
// identical var for the full import-cycle-avoidance reasoning
// (internal/integration.DispatchOutbound's own inbound side calls
// CreateCustomer/UpdateCustomer for Part 3's "create/update by natural
// key" logic, so this package cannot import internal/integration back).
// Wired to the real implementation exactly once, at startup, via
// SetOutboundDispatcher (cmd/server/main.go).
var outboundDispatch = func(pool *pgxpool.Pool, firmID uuid.UUID, sourceEntity string, payload map[string]any) {}

// SetOutboundDispatcher wires outboundDispatch - see that var's own doc
// comment.
func SetOutboundDispatcher(f func(pool *pgxpool.Pool, firmID uuid.UUID, sourceEntity string, payload map[string]any)) {
	outboundDispatch = f
}

// derefStrAny returns nil for a nil pointer, or the pointed-to string
// otherwise - see internal/inventory/outbound_hook.go's identical
// helper for why this conversion matters (outboundDispatch's payload is
// walked programmatically before any JSON marshaling happens).
func derefStrAny(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
