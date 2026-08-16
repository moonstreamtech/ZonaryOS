// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package crm

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// TimelineEntryKind distinguishes an Interaction row from an Opportunity
// row inside a merged TimelineEntry - the caller (the frontend's timeline
// tab) switches on this to render each entry's own shape.
type TimelineEntryKind string

const (
	TimelineEntryKindInteraction TimelineEntryKind = "interaction"
	TimelineEntryKindOpportunity TimelineEntryKind = "opportunity"
)

// TimelineEntry is one row in a customer's merged timeline - either an
// Interaction or an Opportunity, never both (Kind says which; the other
// pointer is nil). Date is what the feed sorts by: an interaction's own
// `date` column for interactions, an opportunity's `created_at` for
// opportunities (an opportunity has no single natural "event date" the
// way a logged call/meeting does - its creation is the one timeline-worthy
// moment this batch tracks; stage CHANGES aren't logged as separate
// events, only interactions explicitly linked to an opportunity are).
type TimelineEntry struct {
	Kind        TimelineEntryKind
	Date        time.Time
	Interaction *Interaction
	Opportunity *Opportunity
}

// GetCustomerTimeline returns customerID's interactions and opportunities
// merged into one feed, sorted by date, newest first.
//
// Merge design: two independent, plain SELECTs - one against
// crm_interactions, one against crm_opportunities, each in its own
// natural shape (no shared column list, no NULL-padding) - fetched into
// Go slices and merged with a single sort.Slice by Date afterward. A
// literal `SELECT ... FROM crm_interactions UNION ALL SELECT ... FROM
// crm_opportunities` was deliberately rejected: PostgreSQL's UNION ALL
// requires both branches to project the SAME number of columns in
// compatible types, so the two tables' genuinely different shapes
// (crm_interactions' type/summary/outcome/next_action vs.
// crm_opportunities' value/stage/probability/assigned_to_person_id)
// would each need to be padded out to the union of both column sets,
// NULL in whichever branch doesn't have that column - a query that grows
// a new NULL-padded column every time either table gains a field, and
// reads badly at the call site (a caller has to distinguish "this
// interaction's outcome is genuinely NULL" from "this row is an
// opportunity, outcome was never applicable"). Two typed queries plus an
// in-memory merge sidesteps all of that: each query stays exactly as
// simple as it would be standalone, and the merge step is one sort, not
// a schema decision. Member-gated.
func GetCustomerTimeline(ctx context.Context, pool *pgxpool.Pool, firmID, userID, customerID uuid.UUID) ([]TimelineEntry, error) {
	var entries []TimelineEntry
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		interactionRows, err := tx.Query(ctx, `SELECT `+interactionColumns+` FROM crm_interactions WHERE firm_id = $1 AND customer_id = $2`, firmID, customerID)
		if err != nil {
			return fmt.Errorf("list interactions for timeline: %w", err)
		}
		for interactionRows.Next() {
			i, err := scanInteractionRow(interactionRows)
			if err != nil {
				interactionRows.Close()
				return err
			}
			entries = append(entries, TimelineEntry{Kind: TimelineEntryKindInteraction, Date: i.Date, Interaction: &i})
		}
		if err := interactionRows.Err(); err != nil {
			interactionRows.Close()
			return err
		}
		interactionRows.Close()

		opportunityRows, err := tx.Query(ctx, `SELECT `+opportunityColumns+` FROM crm_opportunities WHERE firm_id = $1 AND customer_id = $2`, firmID, customerID)
		if err != nil {
			return fmt.Errorf("list opportunities for timeline: %w", err)
		}
		for opportunityRows.Next() {
			o, err := scanOpportunityRow(opportunityRows)
			if err != nil {
				opportunityRows.Close()
				return err
			}
			entries = append(entries, TimelineEntry{Kind: TimelineEntryKindOpportunity, Date: o.CreatedAt, Opportunity: &o})
		}
		if err := opportunityRows.Err(); err != nil {
			opportunityRows.Close()
			return err
		}
		opportunityRows.Close()

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Date.After(entries[j].Date) })
	return entries, nil
}
