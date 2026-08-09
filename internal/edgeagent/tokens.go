// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package edgeagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
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

const tokenAuditEntityType = "edge_agent_token"

const issueTokenAuditAction = "issue"

// tokenPrefix marks a value as an edge agent bearer token at a glance
// (e.g. in logs or a pasted support message) - the same "labeled secret"
// convention GitHub/Stripe tokens use, purely cosmetic: the random suffix
// alone carries all the entropy this scheme relies on.
const tokenPrefix = "eat_"

// TokenSummary is one edge_agent_tokens row with the secret itself
// deliberately absent - what ListTokens returns, and what IssueToken
// returns alongside the one-time plaintext.
type TokenSummary struct {
	ID         uuid.UUID
	AgentID    uuid.UUID
	Name       string
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	CreatedAt  time.Time
}

// generateToken returns a fresh, high-entropy plaintext bearer token (32
// random bytes, base64url-encoded, tokenPrefix-labeled) and its SHA-256
// hex digest - the only form ever persisted (edge_agent_tokens.token_hash).
func generateToken() (plaintext, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	plaintext = tokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	hash = hashToken(plaintext)
	return plaintext, hash, nil
}

// hashToken is the one place a presented token is turned into the form
// stored in / looked up against edge_agent_tokens.token_hash.
func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// IssueTokenInput is IssueToken's request shape.
type IssueTokenInput struct {
	Name      string
	ExpiresAt *time.Time
}

// IssueToken mints a new bearer token for agentID within firmID, storing
// only its hash, and returns the plaintext value exactly once - the
// caller is responsible for handing it to the physical agent; ZonaryOS
// itself never has the plaintext again after this call returns.
// Owner-gated, same tier as CreateAgent.
func IssueToken(ctx context.Context, pool *pgxpool.Pool, firmID, userID, agentID uuid.UUID, input IssueTokenInput) (plaintext string, summary TokenSummary, err error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return "", TokenSummary{}, fmt.Errorf("%w: token name must not be empty", ErrInvalidAgent)
	}

	plaintext, hash, err := generateToken()
	if err != nil {
		return "", TokenSummary{}, err
	}

	txErr := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
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

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM edge_agents WHERE id = $1 AND firm_id = $2)`, agentID, firmID).Scan(&exists); err != nil {
			return fmt.Errorf("check edge agent: %w", err)
		}
		if !exists {
			return ErrAgentNotFound
		}

		summary = TokenSummary{AgentID: agentID, Name: name, ExpiresAt: input.ExpiresAt}
		if err := tx.QueryRow(ctx, `
			INSERT INTO edge_agent_tokens (firm_id, agent_id, token_hash, name, expires_at)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, created_at
		`, firmID, agentID, hash, name, input.ExpiresAt).Scan(&summary.ID, &summary.CreatedAt); err != nil {
			return fmt.Errorf("insert edge agent token: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, summary.ID, tokenAuditEntityType, issueTokenAuditAction, map[string]any{
			"agentId": agentID.String(),
			"name":    name,
		})
	})
	if txErr != nil {
		return "", TokenSummary{}, txErr
	}
	return plaintext, summary, nil
}

// ListTokens lists agentID's tokens within firmID, without their secrets.
// Owner-gated, same tier as IssueToken - token metadata (name, expiry,
// last use) is credential-management information, not ordinary read data.
func ListTokens(ctx context.Context, pool *pgxpool.Pool, firmID, userID, agentID uuid.UUID) ([]TokenSummary, error) {
	var tokens []TokenSummary
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

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM edge_agents WHERE id = $1 AND firm_id = $2)`, agentID, firmID).Scan(&exists); err != nil {
			return fmt.Errorf("check edge agent: %w", err)
		}
		if !exists {
			return ErrAgentNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, agent_id, name, last_used_at, expires_at, created_at
			FROM edge_agent_tokens WHERE agent_id = $1 AND firm_id = $2 ORDER BY created_at DESC
		`, agentID, firmID)
		if err != nil {
			return fmt.Errorf("list edge agent tokens: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var t TokenSummary
			if err := rows.Scan(&t.ID, &t.AgentID, &t.Name, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
				return fmt.Errorf("scan edge agent token: %w", err)
			}
			tokens = append(tokens, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return tokens, nil
}

// AuthenticatedAgent is what a validated token's own request context
// carries: which agent, in which firm, presented it - never anything the
// caller supplied, so a heartbeat/event handler has no client-controlled
// firmID/agentID field to trust in the first place (see TokenMiddleware's
// doc comment).
type AuthenticatedAgent struct {
	FirmID  uuid.UUID
	AgentID uuid.UUID
}

// withTokenTx is the token path's equivalent of db.WithFirmContext/
// db.WithInviteTokenContext: it authenticates rawToken against
// edge_agent_tokens (via the app_current_edge_token_hash() RLS carve-out,
// migrations/0018_edge_agent_protocol.up.sql), then - and only once that
// lookup has resolved a real firm_id - sets app.current_firm_id for the
// remainder of the same transaction before calling fn, the same manual
// two-step sequencing db.WithFirmContext's own doc comment attributes to
// internal/wizard.CreateDefaultFirm and internal/invite.Accept.
//
// The lookup itself is a plain indexed-hash-equality SELECT (token_hash
// is UNIQUE) - safe because token_hash is a digest of a 32-byte random
// secret, so equality timing leaks nothing about the plaintext (the same
// "compare hashes, not secrets" reasoning production API-key schemes
// rely on). subtle.ConstantTimeCompare on the fetched vs. recomputed hash
// bytes is then applied as an explicit, literal constant-time comparison
// before trusting the row - defense in depth on top of the SQL equality
// filter, not a substitute for it.
func withTokenTx(ctx context.Context, pool *pgxpool.Pool, rawToken string, fn func(ctx context.Context, tx pgx.Tx, agent AuthenticatedAgent) error) error {
	hash := hashToken(rawToken)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_edge_token_hash', $1, true)", hash); err != nil {
		return fmt.Errorf("set app.current_edge_token_hash: %w", err)
	}

	var tokenID, firmID, agentID uuid.UUID
	var storedHash string
	var expiresAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, firm_id, agent_id, token_hash, expires_at FROM edge_agent_tokens WHERE token_hash = $1
	`, hash).Scan(&tokenID, &firmID, &agentID, &storedHash, &expiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrInvalidToken
		}
		return fmt.Errorf("lookup edge agent token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hash)) != 1 {
		return ErrInvalidToken
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		return ErrInvalidToken
	}

	// Only now, with a validated token's real firm_id in hand, is the
	// ordinary tenant-scoping session setting established - every write
	// fn performs from here on is governed by the same firm_id-scoped
	// RLS policy every other WithFirmContext caller relies on.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_firm_id', $1, true)", firmID.String()); err != nil {
		return fmt.Errorf("set app.current_firm_id: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE edge_agent_tokens SET last_used_at = now() WHERE id = $1`, tokenID); err != nil {
		return fmt.Errorf("touch edge agent token: %w", err)
	}

	if err := fn(ctx, tx, AuthenticatedAgent{FirmID: firmID, AgentID: agentID}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
