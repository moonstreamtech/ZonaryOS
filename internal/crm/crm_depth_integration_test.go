// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package crm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/crm"
)

// TestGetCustomerTimeline_MergesInteractionsAndOpportunitiesSortedByDate
// confirms GetCustomerTimeline's own merge design: interactions and
// opportunities, fetched independently, come back as ONE feed sorted by
// date, newest first.
func TestGetCustomerTimeline_MergesInteractionsAndOpportunitiesSortedByDate(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "timeline-owner")
	customer, err := crm.CreateCustomer(ctx, appPool, firmID, ownerID, crm.CreateCustomerInput{Name: "Acme Corp"})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}

	opp, err := crm.CreateOpportunity(ctx, appPool, firmID, ownerID, crm.CreateOpportunityInput{
		CustomerID: customer.ID, Name: "Acme Deal", Value: "1000.00",
	})
	if err != nil {
		t.Fatalf("CreateOpportunity: %v", err)
	}

	older := time.Now().Add(-48 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	if _, err := crm.CreateInteraction(ctx, appPool, firmID, ownerID, crm.CreateInteractionInput{
		CustomerID: customer.ID, Type: "call", Date: older, Summary: "Intro call",
	}); err != nil {
		t.Fatalf("CreateInteraction (older): %v", err)
	}
	if _, err := crm.CreateInteraction(ctx, appPool, firmID, ownerID, crm.CreateInteractionInput{
		CustomerID: customer.ID, Type: "demo", Date: newer, Summary: "Product demo", OpportunityID: &opp.ID,
	}); err != nil {
		t.Fatalf("CreateInteraction (newer): %v", err)
	}

	entries, err := crm.GetCustomerTimeline(ctx, appPool, firmID, ownerID, customer.ID)
	if err != nil {
		t.Fatalf("GetCustomerTimeline: %v", err)
	}
	// 2 interactions + 1 opportunity (created "now", so newest).
	if len(entries) != 3 {
		t.Fatalf("expected 3 timeline entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Kind != crm.TimelineEntryKindOpportunity {
		t.Errorf("expected the newest entry to be the just-created opportunity, got kind %q", entries[0].Kind)
	}
	if entries[1].Kind != crm.TimelineEntryKindInteraction || entries[1].Interaction.Summary != "Product demo" {
		t.Errorf("expected the second entry to be the newer interaction, got %+v", entries[1])
	}
	if entries[2].Kind != crm.TimelineEntryKindInteraction || entries[2].Interaction.Summary != "Intro call" {
		t.Errorf("expected the third (oldest) entry to be the older interaction, got %+v", entries[2])
	}
	for i := 0; i+1 < len(entries); i++ {
		if entries[i].Date.Before(entries[i+1].Date) {
			t.Fatalf("expected entries sorted newest-first, entry %d (%v) is before entry %d (%v)", i, entries[i].Date, i+1, entries[i+1].Date)
		}
	}
}

// TestUpdateOpportunityStage_RefusesToReopenAClosedDeal confirms the
// stage-transition validation this batch's design brief asks for: once
// an opportunity is won or lost, it can't move to any other stage.
func TestUpdateOpportunityStage_RefusesToReopenAClosedDeal(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "stage-owner")
	customer, err := crm.CreateCustomer(ctx, appPool, firmID, ownerID, crm.CreateCustomerInput{Name: "Acme Corp"})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	opp, err := crm.CreateOpportunity(ctx, appPool, firmID, ownerID, crm.CreateOpportunityInput{CustomerID: customer.ID, Name: "Deal"})
	if err != nil {
		t.Fatalf("CreateOpportunity: %v", err)
	}

	// A normal forward move succeeds.
	opp, err = crm.UpdateOpportunityStage(ctx, appPool, firmID, ownerID, opp.ID, crm.OpportunityStageQualified)
	if err != nil {
		t.Fatalf("UpdateOpportunityStage (prospect -> qualified): %v", err)
	}
	if opp.Stage != crm.OpportunityStageQualified {
		t.Fatalf("expected stage qualified, got %q", opp.Stage)
	}

	// Closing the deal.
	opp, err = crm.UpdateOpportunityStage(ctx, appPool, firmID, ownerID, opp.ID, crm.OpportunityStageWon)
	if err != nil {
		t.Fatalf("UpdateOpportunityStage (qualified -> won): %v", err)
	}
	if opp.Stage != crm.OpportunityStageWon {
		t.Fatalf("expected stage won, got %q", opp.Stage)
	}

	// A won deal can't move anywhere, including "back" to an earlier
	// stage or "forward" to lost.
	if _, err := crm.UpdateOpportunityStage(ctx, appPool, firmID, ownerID, opp.ID, crm.OpportunityStageLost); !errors.Is(err, crm.ErrInvalidOpportunityTransition) {
		t.Fatalf("expected ErrInvalidOpportunityTransition reopening a won deal, got %v", err)
	}
	if _, err := crm.UpdateOpportunityStage(ctx, appPool, firmID, ownerID, opp.ID, crm.OpportunityStageProspect); !errors.Is(err, crm.ErrInvalidOpportunityTransition) {
		t.Fatalf("expected ErrInvalidOpportunityTransition reopening a won deal, got %v", err)
	}

	// An unknown stage value is rejected structurally, before the
	// terminal-stage check even runs.
	if _, err := crm.UpdateOpportunityStage(ctx, appPool, firmID, ownerID, opp.ID, crm.OpportunityStage("bogus")); !errors.Is(err, crm.ErrInvalidOpportunity) {
		t.Fatalf("expected ErrInvalidOpportunity for an unknown stage, got %v", err)
	}
}
