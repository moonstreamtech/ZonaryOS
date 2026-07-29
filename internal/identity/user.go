// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package identity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolveOrCreateUser upserts id into the global (non-tenant-scoped) users
// table by keycloak_subject and returns its row ID. This is the sole point
// where Keycloak identity data (email, display name) is written into
// ZonaryOS's own database - it never touches firm membership or roles.
func ResolveOrCreateUser(ctx context.Context, pool *pgxpool.Pool, id Identity) (uuid.UUID, error) {
	var userID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (keycloak_subject) DO UPDATE
			SET email = EXCLUDED.email, display_name = EXCLUDED.display_name
		RETURNING id
	`, id.Subject, id.Email, id.DisplayName).Scan(&userID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("resolve user: %w", err)
	}
	return userID, nil
}
