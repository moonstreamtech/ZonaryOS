// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package absence is the HR depth batch's absence-request core:
// absences (vacation/sick/unpaid/maternity/other), backed for real by the
// existing internal/workflow engine's Absence Approval workflow
// (workflow.AbsenceApprovalSpec) - not a bespoke parallel approval
// mechanism. See CreateAbsence's own doc comment for the full design.
package absence

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

const dateLayout = "2006-01-02"

const absenceAuditEntityType = "absence"
const (
	createAbsenceAuditAction  = "create"
	updateAbsenceAuditAction  = "update"
	deleteAbsenceAuditAction  = "delete"
	approveAbsenceAuditAction = "approve"
	rejectAbsenceAuditAction  = "reject"
)

// Type is absences.type's closed vocabulary - mirrors the migration's own
// CHECK constraint.
type Type string

const (
	TypeVacation  Type = "vacation"
	TypeSick      Type = "sick"
	TypeUnpaid    Type = "unpaid"
	TypeMaternity Type = "maternity"
	TypeOther     Type = "other"
)

func (t Type) valid() bool {
	switch t {
	case TypeVacation, TypeSick, TypeUnpaid, TypeMaternity, TypeOther:
		return true
	default:
		return false
	}
}

// Status is absences.status's closed vocabulary.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

// Absence is one absences row.
type Absence struct {
	ID                 uuid.UUID
	FirmID             uuid.UUID
	PersonID           uuid.UUID
	WorkflowInstanceID *uuid.UUID
	RequestedBy        uuid.UUID
	Type               Type
	StartDate          time.Time
	EndDate            time.Time
	Status             Status
	ApprovedBy         *uuid.UUID
	Notes              *string
	CreatedAt          time.Time
}

// CreateAbsenceInput is CreateAbsence's request shape.
type CreateAbsenceInput struct {
	PersonID  uuid.UUID
	Type      Type
	StartDate string // "YYYY-MM-DD", required
	EndDate   string // "YYYY-MM-DD", required
	Notes     string
}

// CreateAbsence inserts a new absences row (status 'pending') for
// input.PersonID within firmID, then backs it with a real Absence
// Approval workflow instance (workflow.AbsenceApprovalSpec) and requests
// approval on it - the SAME pending_approvals + notification mechanism
// (workflow.RequestApproval) every other approval in this codebase goes
// through.
//
// Member-gated (any member can request their own absence - the requester
// doesn't need to be an owner; approving/rejecting is what's owner-gated,
// see Approve/Reject below).
//
// Atomicity - deliberately NOT one all-or-nothing operation: the
// absences-row insert runs inside its own transaction
// (zdb.WithFirmContext); workflow.CreateInstance then runs OUTSIDE that
// transaction and manages its own (it isn't tx-composable - there is no
// *Tx variant of CreateInstance today), so this function's own two
// database-visible steps can't be wrapped in one commit/rollback
// boundary. Concretely: if CreateInstance (or the RequestApproval call
// after it) fails AFTER the absence row has already committed, the
// absence is left in the database, 'pending', with no backing instance
// and no pending_approvals row - a real limitation of
// workflow.CreateInstance's own current API shape (a *Tx variant would
// close this gap; out of scope for this batch, worth a follow-up). This
// function's own choice for that failure mode: treat it as a hard error
// returned to the caller (not a silently-successful "your absence exists,
// good luck" response) - the HTTP caller sees a 500 and can retry the
// whole CreateAbsence call; a naive retry would leave a second, duplicate
// absences row behind (there's no idempotency key here), which is an
// accepted rough edge of this interim design, not something this batch
// attempts to solve.
func CreateAbsence(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateAbsenceInput) (Absence, error) {
	if !input.Type.valid() {
		return Absence{}, fmt.Errorf("%w: type must be one of vacation/sick/unpaid/maternity/other", ErrInvalidAbsence)
	}
	if input.PersonID == uuid.Nil {
		return Absence{}, fmt.Errorf("%w: personId must be set", ErrInvalidAbsence)
	}
	if input.StartDate == "" || input.EndDate == "" {
		return Absence{}, fmt.Errorf("%w: startDate and endDate must both be set", ErrInvalidAbsence)
	}
	start, err := time.Parse(dateLayout, input.StartDate)
	if err != nil {
		return Absence{}, fmt.Errorf("%w: startDate %q must be in YYYY-MM-DD format", ErrInvalidAbsence, input.StartDate)
	}
	end, err := time.Parse(dateLayout, input.EndDate)
	if err != nil {
		return Absence{}, fmt.Errorf("%w: endDate %q must be in YYYY-MM-DD format", ErrInvalidAbsence, input.EndDate)
	}
	if end.Before(start) {
		return Absence{}, fmt.Errorf("%w: endDate must not be before startDate", ErrInvalidAbsence)
	}
	var notes *string
	if n := strings.TrimSpace(input.Notes); n != "" {
		notes = &n
	}

	var abs Absence
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		personExists, err := hr.PersonExistsTx(ctx, tx, firmID, input.PersonID)
		if err != nil {
			return err
		}
		if !personExists {
			return fmt.Errorf("%w: person not found", ErrInvalidAbsence)
		}

		abs = Absence{FirmID: firmID, PersonID: input.PersonID, RequestedBy: userID, Type: input.Type, StartDate: start, EndDate: end, Status: StatusPending, Notes: notes}
		if err := tx.QueryRow(ctx, `
			INSERT INTO absences (firm_id, person_id, requested_by, type, start_date, end_date, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id, created_at
		`, firmID, input.PersonID, userID, string(input.Type), start, end, notes).Scan(&abs.ID, &abs.CreatedAt); err != nil {
			return fmt.Errorf("insert absence: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, abs.ID, absenceAuditEntityType, createAbsenceAuditAction, map[string]any{
			"personId": input.PersonID.String(), "type": string(input.Type),
		})
	})
	if err != nil {
		return Absence{}, err
	}

	definition, err := workflow.LookupDefinitionByKey(ctx, pool, firmID, userID, workflow.AbsenceApprovalKey)
	if err != nil {
		return Absence{}, fmt.Errorf("look up absence approval workflow definition (absence %s was created but has no backing approval): %w", abs.ID, err)
	}
	instanceID, err := workflow.CreateInstance(ctx, pool, firmID, userID, definition.ID, map[string]any{"absence_id": abs.ID.String()})
	if err != nil {
		return Absence{}, fmt.Errorf("create absence approval workflow instance (absence %s was created but has no backing approval): %w", abs.ID, err)
	}

	if err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE absences SET workflow_instance_id = $1 WHERE id = $2 AND firm_id = $3`, instanceID, abs.ID, firmID)
		return err
	}); err != nil {
		return Absence{}, fmt.Errorf("link absence %s to workflow instance %s: %w", abs.ID, instanceID, err)
	}
	abs.WorkflowInstanceID = &instanceID

	if _, err := workflow.RequestApproval(ctx, pool, firmID, userID, instanceID, workflow.AbsenceApprovalKey, "requested", "approve"); err != nil {
		return Absence{}, fmt.Errorf("request approval for absence %s: %w", abs.ID, err)
	}

	return abs, nil
}

// ListAbsences returns firmID's absences, most recent start date first.
// Member-gated read.
func ListAbsences(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Absence, error) {
	var absences []Absence
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, person_id, workflow_instance_id, requested_by, type, start_date, end_date, status, approved_by, notes, created_at
			FROM absences WHERE firm_id = $1 ORDER BY start_date DESC
		`, firmID)
		if err != nil {
			return fmt.Errorf("list absences: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAbsence(rows)
			if err != nil {
				return err
			}
			absences = append(absences, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return absences, nil
}

func scanAbsence(row pgx.Row) (Absence, error) {
	var a Absence
	var typeStr, statusStr string
	if err := row.Scan(&a.ID, &a.FirmID, &a.PersonID, &a.WorkflowInstanceID, &a.RequestedBy, &typeStr, &a.StartDate, &a.EndDate, &statusStr, &a.ApprovedBy, &a.Notes, &a.CreatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return Absence{}, ErrAbsenceNotFound
		}
		return Absence{}, fmt.Errorf("scan absence: %w", err)
	}
	a.Type = Type(typeStr)
	a.Status = Status(statusStr)
	return a, nil
}

// GetAbsence resolves one absence by id within firmID. Member-gated.
func GetAbsence(ctx context.Context, pool *pgxpool.Pool, firmID, userID, absenceID uuid.UUID) (Absence, error) {
	var abs Absence
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		var err2 error
		abs, err2 = getAbsenceTx(ctx, tx, firmID, absenceID)
		return err2
	})
	if err != nil {
		return Absence{}, err
	}
	return abs, nil
}

// getAbsenceTx loads absenceID within firmID.
//
// ciaudit:ignore-firmid-check: internal helper, only called by GetAbsence/
// UpdateAbsence/DeleteAbsence/Approve/Reject, each of which has already
// run permission.IsMember before reaching this call - firmID here scopes
// the query, not a fresh authorization decision.
func getAbsenceTx(ctx context.Context, tx pgx.Tx, firmID, absenceID uuid.UUID) (Absence, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, firm_id, person_id, workflow_instance_id, requested_by, type, start_date, end_date, status, approved_by, notes, created_at
		FROM absences WHERE id = $1 AND firm_id = $2
	`, absenceID, firmID)
	return scanAbsence(row)
}

// UpdateAbsenceInput is UpdateAbsence's request shape - only while the
// absence is still 'pending', per the task spec.
type UpdateAbsenceInput struct {
	Type      *Type
	StartDate *string
	EndDate   *string
	Notes     *string
}

// UpdateAbsence changes absenceID's mutable fields within firmID, only
// while it's still 'pending' (ErrAbsenceNotPending otherwise). Restricted
// to the absence's own requester, or an owner - same "own + owner
// override" shape internal/timetracking.checkEntryEditableTx uses.
func UpdateAbsence(ctx context.Context, pool *pgxpool.Pool, firmID, userID, absenceID uuid.UUID, input UpdateAbsenceInput) (Absence, error) {
	var abs Absence
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		abs, err = getAbsenceTx(ctx, tx, firmID, absenceID)
		if err != nil {
			return err
		}
		if abs.Status != StatusPending {
			return ErrAbsenceNotPending
		}
		if err := checkAbsenceEditableTx(ctx, tx, firmID, userID, abs); err != nil {
			return err
		}

		absType := abs.Type
		if input.Type != nil {
			if !input.Type.valid() {
				return fmt.Errorf("%w: type must be one of vacation/sick/unpaid/maternity/other", ErrInvalidAbsence)
			}
			absType = *input.Type
		}
		start := abs.StartDate
		if input.StartDate != nil {
			start, err = time.Parse(dateLayout, *input.StartDate)
			if err != nil {
				return fmt.Errorf("%w: startDate must be in YYYY-MM-DD format", ErrInvalidAbsence)
			}
		}
		end := abs.EndDate
		if input.EndDate != nil {
			end, err = time.Parse(dateLayout, *input.EndDate)
			if err != nil {
				return fmt.Errorf("%w: endDate must be in YYYY-MM-DD format", ErrInvalidAbsence)
			}
		}
		if end.Before(start) {
			return fmt.Errorf("%w: endDate must not be before startDate", ErrInvalidAbsence)
		}
		notes := abs.Notes
		if input.Notes != nil {
			n := strings.TrimSpace(*input.Notes)
			if n == "" {
				notes = nil
			} else {
				notes = &n
			}
		}

		row := tx.QueryRow(ctx, `
			UPDATE absences SET type = $1, start_date = $2, end_date = $3, notes = $4
			WHERE id = $5 AND firm_id = $6
			RETURNING id, firm_id, person_id, workflow_instance_id, requested_by, type, start_date, end_date, status, approved_by, notes, created_at
		`, string(absType), start, end, notes, absenceID, firmID)
		abs, err = scanAbsence(row)
		if err != nil {
			return fmt.Errorf("update absence: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, abs.ID, absenceAuditEntityType, updateAbsenceAuditAction, map[string]any{"type": string(absType)})
	})
	if err != nil {
		return Absence{}, err
	}
	return abs, nil
}

// checkAbsenceEditableTx enforces "own absence, or owner" - mirrors
// internal/timetracking.checkEntryEditableTx exactly.
func checkAbsenceEditableTx(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID, abs Absence) error {
	if abs.RequestedBy == userID {
		return nil
	}
	isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
	if err != nil {
		return err
	}
	if !isOwner {
		return ErrNotAbsenceOwner
	}
	return nil
}

// DeleteAbsence removes absenceID within firmID, only while it's still
// 'pending'. Restricted to the absence's own requester, or an owner -
// same rule as UpdateAbsence.
func DeleteAbsence(ctx context.Context, pool *pgxpool.Pool, firmID, userID, absenceID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		abs, err := getAbsenceTx(ctx, tx, firmID, absenceID)
		if err != nil {
			return err
		}
		if abs.Status != StatusPending {
			return ErrAbsenceNotPending
		}
		if err := checkAbsenceEditableTx(ctx, tx, firmID, userID, abs); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `DELETE FROM absences WHERE id = $1 AND firm_id = $2`, absenceID, firmID); err != nil {
			return fmt.Errorf("delete absence: %w", err)
		}
		return auditlog.Write(ctx, tx, firmID, userID, absenceID, absenceAuditEntityType, deleteAbsenceAuditAction, map[string]any{})
	})
}

// findPendingApprovalTx looks up the pending pending_approvals row for
// instanceID - a simple read-only lookup, fine to query directly since
// it's just resolving an id, gated by the caller (Approve/Reject) already
// having checked the caller is an owner before reaching here.
func findPendingApprovalTx(ctx context.Context, tx pgx.Tx, instanceID uuid.UUID) (uuid.UUID, error) {
	var pendingApprovalID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM pending_approvals WHERE instance_id = $1 AND status = 'pending'
	`, instanceID).Scan(&pendingApprovalID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.UUID{}, ErrNoPendingApproval
		}
		return uuid.UUID{}, fmt.Errorf("look up pending approval: %w", err)
	}
	return pendingApprovalID, nil
}

// ciaudit:ignore-firmid-check: thin wrapper around resolveAbsence, which
// itself runs the real permission.IsMember/IsOwner checks in its own
// first transaction before doing anything else.
//
// Approve approves absenceID within firmID - owner-gated. Two phases,
// deliberately not one transaction, mirroring workflow.Approve's own
// documented two-phase reasoning: the pending_approvals lookup and the
// real workflow.Approve call (which itself runs ExecuteTransition, moving
// the backing instance from 'requested' to 'approved' for real) happen
// first; absences.status is then updated separately to stay in sync with
// what the instance's own state now says. If the second UPDATE somehow
// failed, the instance itself would already be 'approved' with
// absences.status still 'pending' - a narrow inconsistency window,
// logged rather than silently ignored, same posture as this package's
// other documented rough edges.
func Approve(ctx context.Context, pool *pgxpool.Pool, firmID, userID, absenceID uuid.UUID) (Absence, error) {
	return resolveAbsence(ctx, pool, firmID, userID, absenceID, StatusApproved, func(ctx context.Context, pendingApprovalID, instanceID uuid.UUID) error {
		_, err := workflow.Approve(ctx, pool, firmID, userID, pendingApprovalID)
		return err
	})
}

// ciaudit:ignore-firmid-check: thin wrapper around resolveAbsence, which
// itself runs the real permission.IsMember/IsOwner checks in its own
// first transaction before doing anything else.
//
// Reject rejects absenceID within firmID - owner-gated. Calls
// workflow.Reject (marks the pending_approvals row 'rejected' and
// notifies the requester) AND drives the backing instance to its own
// 'rejected' terminal state via workflow.ExecuteTransition, so
// pending_approvals and the instance's own state stay consistent with
// each other - workflow.Reject alone only resolves the approval record,
// it does not itself execute any transition (see that function's own doc
// comment), so without this extra call the instance would be stuck
// forever at 'requested' even though this absence is fully resolved.
func Reject(ctx context.Context, pool *pgxpool.Pool, firmID, userID, absenceID uuid.UUID) (Absence, error) {
	return resolveAbsence(ctx, pool, firmID, userID, absenceID, StatusRejected, func(ctx context.Context, pendingApprovalID, instanceID uuid.UUID) error {
		if _, err := workflow.Reject(ctx, pool, firmID, userID, pendingApprovalID); err != nil {
			return err
		}
		return workflow.ExecuteTransition(ctx, pool, firmID, userID, instanceID, "reject", nil)
	})
}

// resolveAbsence is Approve/Reject's shared shape: owner-gated, look up
// the absence and its pending approval, run the workflow-level side
// effect, then update absences.status/approved_by to match.
func resolveAbsence(ctx context.Context, pool *pgxpool.Pool, firmID, userID, absenceID uuid.UUID, newStatus Status, runWorkflowSideEffect func(ctx context.Context, pendingApprovalID, instanceID uuid.UUID) error) (Absence, error) {
	var abs Absence
	var instanceID uuid.UUID
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

		abs, err = getAbsenceTx(ctx, tx, firmID, absenceID)
		if err != nil {
			return err
		}
		if abs.Status != StatusPending {
			return ErrAbsenceNotPending
		}
		if abs.WorkflowInstanceID == nil {
			return ErrNoPendingApproval
		}
		instanceID = *abs.WorkflowInstanceID
		return nil
	})
	if err != nil {
		return Absence{}, err
	}

	// The pending_approvals lookup and the real Approve/Reject call both
	// need their own transaction (workflow.Approve/Reject manage their
	// own), so they run here, outside the membership/ownership check's
	// transaction above - mirroring workflow.Approve's own two-phase
	// shape (approvals.go's own doc comment).
	var pendingApprovalID uuid.UUID
	if err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		pendingApprovalID, err = findPendingApprovalTx(ctx, tx, instanceID)
		return err
	}); err != nil {
		return Absence{}, err
	}

	if err := runWorkflowSideEffect(ctx, pendingApprovalID, instanceID); err != nil {
		return Absence{}, err
	}

	auditAction := approveAbsenceAuditAction
	if newStatus == StatusRejected {
		auditAction = rejectAbsenceAuditAction
	}
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE absences SET status = $1, approved_by = $2
			WHERE id = $3 AND firm_id = $4
			RETURNING id, firm_id, person_id, workflow_instance_id, requested_by, type, start_date, end_date, status, approved_by, notes, created_at
		`, string(newStatus), userID, absenceID, firmID)
		var err error
		abs, err = scanAbsence(row)
		if err != nil {
			return fmt.Errorf("sync absence status: %w", err)
		}
		return auditlog.Write(ctx, tx, firmID, userID, abs.ID, absenceAuditEntityType, auditAction, map[string]any{"status": string(newStatus)})
	})
	if err != nil {
		// The instance/pending_approvals side effect already succeeded by
		// this point - absences.status simply failed to sync. Logged, not
		// silently swallowed, per this package's documented rough-edge
		// posture (see this function's own doc comment).
		slog.Error("absence: workflow side effect succeeded but syncing absences.status failed", "absenceId", absenceID, "err", err)
		return Absence{}, err
	}
	return abs, nil
}
