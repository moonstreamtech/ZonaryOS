// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package payroll

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// payrollClosedSourceType is what the DR Salary Expense / CR Salaries
// Payable journal entry ClosePeriod posts records journal_entries.source_type
// as - mirrors internal/invoicing's own invoicePaidSourceType convention
// ("invoice"): here the source is the payroll period that closed, not a
// workflow instance.
const payrollClosedSourceType = "payroll_period"

// ClosePeriod transitions periodID from 'open' to 'closed' and - in the
// SAME transaction - posts a single balanced journal entry summarizing
// the whole period's payroll: DR Salary Expense / CR Salaries Payable,
// each leg equal to the SUM of gross_amount across every entry in the
// period.
//
// Design choice (gross, not net): this batch's task spec explicitly
// allows either "as long as DR == CR and both equal the sum of some
// consistent amount." gross_amount is used here, not net_amount, because
// a single two-legged journal entry can only balance against ONE number -
// using net_amount for both legs would silently drop the deductions
// portion of the period's real payroll cost out of the ledger entirely
// (deductions are stored on payroll_entries.deductions but this batch
// deliberately does no deduction *accounting* - no separate withholding-
// liability accounts are modeled, see this package's own doc comment) -
// gross_amount is what the ledger should show as "what this period's
// payroll cost the firm," with net_amount remaining available on each
// entry as the record of what was actually intended to reach each person.
//
// Atomicity: one transaction, in this order - (1) permission check, (2)
// SELECT ... FOR UPDATE on the period row (claims a row lock, so two
// concurrent ClosePeriod calls serialize instead of both reading 'open'
// and both trying to close), (3) reject if already 'closed'
// (ErrPeriodAlreadyClosed - the FOR UPDATE lock plus this status check is
// what makes a double-close impossible: the second caller's SELECT ... FOR
// UPDATE blocks until the first COMMITs, then sees 'closed' and returns
// the error instead of posting a second journal entry), (4) sum
// gross_amount across the period's entries, (5) if the sum is > 0, post
// the DR/CR journal entry via accounting.PostJournalEntryTx, (6) UPDATE
// the period's status to 'closed', (7) write the close audit_log entry.
// Committing all seven together means a failure anywhere (an unbalanced
// entry, a constraint violation) rolls back the whole close, including
// the status change - the period is left 'open' and safely retryable, and
// there is no code path that can post the journal entry without the
// status flip, or flip the status without the journal entry.
func ClosePeriod(ctx context.Context, pool *pgxpool.Pool, firmID, userID, periodID uuid.UUID) (Period, error) {
	var period Period
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

		var status string
		err = tx.QueryRow(ctx, `
			SELECT id, firm_id, name, period_start, period_end, status, created_at
			FROM payroll_periods WHERE id = $1 AND firm_id = $2
			FOR UPDATE
		`, periodID, firmID).Scan(&period.ID, &period.FirmID, &period.Name, &period.PeriodStart, &period.PeriodEnd, &status, &period.CreatedAt)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrPeriodNotFound
			}
			return fmt.Errorf("look up payroll period: %w", err)
		}
		if PeriodStatus(status) == PeriodStatusClosed {
			return ErrPeriodAlreadyClosed
		}

		// Summed by Postgres's own exact `numeric` arithmetic, never a Go
		// float - same discipline internal/invoicing.RecordPayment's own
		// paid-in-full check follows.
		var totalGross string
		var hasEntries bool
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(gross_amount), 0)::text, COUNT(*) > 0
			FROM payroll_entries WHERE period_id = $1 AND firm_id = $2
		`, periodID, firmID).Scan(&totalGross, &hasEntries); err != nil {
			return fmt.Errorf("sum payroll entries: %w", err)
		}

		if hasEntries {
			lines := []accounting.LineInput{
				{AccountCode: accounting.SalaryExpenseAccountCode, Side: accounting.SideDebit, Amount: totalGross},
				{AccountCode: accounting.SalariesPayableAccountCode, Side: accounting.SideCredit, Amount: totalGross},
			}
			description := fmt.Sprintf("Payroll period %q closed", period.Name)
			if _, err := accounting.PostJournalEntryTx(ctx, tx, firmID, userID, description, payrollClosedSourceType, &periodID, lines); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `UPDATE payroll_periods SET status = 'closed' WHERE id = $1 AND firm_id = $2`, periodID, firmID); err != nil {
			return fmt.Errorf("close payroll period: %w", err)
		}
		period.Status = PeriodStatusClosed

		return auditlog.Write(ctx, tx, firmID, userID, period.ID, periodAuditEntityType, closePeriodAuditAction, map[string]any{
			"totalGross": totalGross,
		})
	})
	if err != nil {
		return Period{}, err
	}
	return period, nil
}
