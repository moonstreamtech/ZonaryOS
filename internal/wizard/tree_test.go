// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package wizard_test

import (
	"errors"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
)

func TestRootNode_IsAQuestionWithTwoAnswers(t *testing.T) {
	if wizard.RootNode.Kind != wizard.NodeQuestion {
		t.Fatalf("expected root node to be a question, got kind %v", wizard.RootNode.Kind)
	}
	if wizard.RootNode.QuestionKey != "doYouSellProductsOrServices" {
		t.Errorf("expected root question key %q, got %q", "doYouSellProductsOrServices", wizard.RootNode.QuestionKey)
	}
	if len(wizard.RootNode.Answers) != 2 {
		t.Fatalf("expected 2 answers at the root, got %d", len(wizard.RootNode.Answers))
	}
}

func TestAnswer_RejectsUnknownAnswerValue(t *testing.T) {
	if _, err := wizard.RootNode.Answer("maybe"); !errors.Is(err, wizard.ErrUnknownAnswer) {
		t.Fatalf("expected ErrUnknownAnswer, got %v", err)
	}
}

func TestAnswer_RejectsAnsweringANonQuestionNode(t *testing.T) {
	action := walk(t, "no", "no", "no", "no")
	if _, err := action.Answer("anything"); !errors.Is(err, wizard.ErrNotQuestion) {
		t.Fatalf("expected ErrNotQuestion, got %v", err)
	}
}

// walk drives the tree from RootNode through the given sequence of
// yes/no answers, failing the test immediately if any answer is rejected
// or the tree runs out of questions before the sequence does.
func walk(t *testing.T, answers ...string) *wizard.Node {
	t.Helper()
	node := wizard.RootNode
	for _, a := range answers {
		next, err := node.Answer(a)
		if err != nil {
			t.Fatalf("Answer(%q): %v", a, err)
		}
		node = next
	}
	return node
}

// TestAnswer_SellsNoSkipsInventoryAndCRMQuestions: answering "no" to root
// question 1 goes straight to root question 2 (purchasing), skipping the
// inventory/CRM sub-questions entirely - Vision §3's ordering principle
// ("universal questions first"), and the design brief's explicit "No ->
// still ask sub-question 3 (tasks/approvals are universal)" note.
func TestAnswer_SellsNoSkipsInventoryAndCRMQuestions(t *testing.T) {
	next, err := wizard.RootNode.Answer("no")
	if err != nil {
		t.Fatalf("Answer(\"no\"): %v", err)
	}
	if next.Kind != wizard.NodeQuestion {
		t.Fatalf("expected the next node to be a question, got kind %v", next.Kind)
	}
	if next.QuestionKey != "doYouPurchaseFromSuppliers" {
		t.Errorf("expected the next question to be doYouPurchaseFromSuppliers, got %q", next.QuestionKey)
	}
}

// TestAnswer_SellsYesAsksInventoryThenCRM covers the sub-branch: "yes" to
// selling opens the inventory question, then the CRM question, before
// continuing to purchasing.
func TestAnswer_SellsYesAsksInventoryThenCRM(t *testing.T) {
	inventoryNode, err := wizard.RootNode.Answer("yes")
	if err != nil {
		t.Fatalf("Answer(\"yes\"): %v", err)
	}
	if inventoryNode.QuestionKey != "doYouTrackPhysicalInventory" {
		t.Errorf("expected doYouTrackPhysicalInventory, got %q", inventoryNode.QuestionKey)
	}

	crmNode, err := inventoryNode.Answer("yes")
	if err != nil {
		t.Fatalf("Answer(\"yes\") on inventory node: %v", err)
	}
	if crmNode.QuestionKey != "doYouManageCustomerRelationships" {
		t.Errorf("expected doYouManageCustomerRelationships, got %q", crmNode.QuestionKey)
	}

	purchaseNode, err := crmNode.Answer("yes")
	if err != nil {
		t.Fatalf("Answer(\"yes\") on crm node: %v", err)
	}
	if purchaseNode.QuestionKey != "doYouPurchaseFromSuppliers" {
		t.Errorf("expected doYouPurchaseFromSuppliers, got %q", purchaseNode.QuestionKey)
	}
}

// TestAnswer_FullSequenceEndsAtManufactureThenAction walks every question
// down one full path and checks the terminal manufacturing question is
// reached last, with "no" resolving to the create-firm action carrying
// the accumulated SeedSelection, and "yes" dead-ending at the existing
// placeholder.
func TestAnswer_FullSequenceEndsAtManufactureThenAction(t *testing.T) {
	manufactureNode := walk(t, "yes", "yes", "yes", "yes", "yes")
	if manufactureNode.QuestionKey != "doYouManufacture" {
		t.Fatalf("expected the 6th node to be doYouManufacture, got %q", manufactureNode.QuestionKey)
	}

	action, err := manufactureNode.Answer("no")
	if err != nil {
		t.Fatalf("Answer(\"no\") on manufacture node: %v", err)
	}
	if action.Kind != wizard.NodeAction {
		t.Fatalf("expected an action node, got kind %v", action.Kind)
	}
	if action.ActionKey != wizard.ActionCreateDefaultFirm {
		t.Errorf("expected action key %q, got %q", wizard.ActionCreateDefaultFirm, action.ActionKey)
	}
	if action.Seed == nil {
		t.Fatal("expected a non-nil Seed on the create-default-firm action node")
	}
	want := wizard.SeedSelection{
		Sells: true, TracksInventory: true, ManagesCRM: true,
		PurchasesFromSuppliers: true, ManagesTasks: true,
	}
	if *action.Seed != want {
		t.Errorf("expected accumulated SeedSelection %+v, got %+v", want, *action.Seed)
	}

	placeholder, err := manufactureNode.Answer("yes")
	if err != nil {
		t.Fatalf("Answer(\"yes\") on manufacture node: %v", err)
	}
	if placeholder.Kind != wizard.NodePlaceholder {
		t.Fatalf("expected a placeholder node, got kind %v", placeholder.Kind)
	}
	if placeholder.PlaceholderKey == "" {
		t.Error("expected a non-empty placeholder key")
	}
}

// TestAnswer_SeedSelectionReflectsEachNoPath is the "no" counterpart of
// the test above: every question answered "no" should leave that
// question's SeedSelection field false, not just default-zero by luck.
func TestAnswer_SeedSelectionReflectsEachNoPath(t *testing.T) {
	manufactureNode := walk(t, "no", "no", "no")
	if manufactureNode.QuestionKey != "doYouManufacture" {
		t.Fatalf("expected doYouManufacture after sells=no, purchase=no, tasks=no, got %q", manufactureNode.QuestionKey)
	}

	action, err := manufactureNode.Answer("no")
	if err != nil {
		t.Fatalf("Answer(\"no\"): %v", err)
	}
	if action.Seed == nil {
		t.Fatal("expected a non-nil Seed")
	}
	if *action.Seed != (wizard.SeedSelection{}) {
		t.Errorf("expected an all-false SeedSelection, got %+v", *action.Seed)
	}
}

func TestLookup_FindsRootAndKeyDescendantNodes(t *testing.T) {
	for _, key := range []string{"root", "sellsYes_inventory", "sellsNo_purchase"} {
		if _, err := wizard.Lookup(key); err != nil {
			t.Errorf("Lookup(%q): %v", key, err)
		}
	}
}

func TestLookup_RejectsUnknownKey(t *testing.T) {
	if _, err := wizard.Lookup("no_such_node"); !errors.Is(err, wizard.ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
}
