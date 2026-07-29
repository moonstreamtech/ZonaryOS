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
	if wizard.RootNode.QuestionKey != "doYouManufacture" {
		t.Errorf("expected root question key %q, got %q", "doYouManufacture", wizard.RootNode.QuestionKey)
	}
	if len(wizard.RootNode.Answers) != 2 {
		t.Fatalf("expected 2 answers at the root, got %d", len(wizard.RootNode.Answers))
	}
}

func TestAnswer_NoLeadsToCreateDefaultFirmAction(t *testing.T) {
	next, err := wizard.RootNode.Answer("no")
	if err != nil {
		t.Fatalf("Answer(\"no\"): %v", err)
	}
	if next.Kind != wizard.NodeAction {
		t.Fatalf("expected an action node, got kind %v", next.Kind)
	}
	if next.ActionKey != wizard.ActionCreateDefaultFirm {
		t.Errorf("expected action key %q, got %q", wizard.ActionCreateDefaultFirm, next.ActionKey)
	}
}

func TestAnswer_YesLeadsToPlaceholder(t *testing.T) {
	next, err := wizard.RootNode.Answer("yes")
	if err != nil {
		t.Fatalf("Answer(\"yes\"): %v", err)
	}
	if next.Kind != wizard.NodePlaceholder {
		t.Fatalf("expected a placeholder node, got kind %v", next.Kind)
	}
	if next.PlaceholderKey == "" {
		t.Error("expected a non-empty placeholder key")
	}
}

func TestAnswer_RejectsUnknownAnswerValue(t *testing.T) {
	if _, err := wizard.RootNode.Answer("maybe"); !errors.Is(err, wizard.ErrUnknownAnswer) {
		t.Fatalf("expected ErrUnknownAnswer, got %v", err)
	}
}

func TestAnswer_RejectsAnsweringANonQuestionNode(t *testing.T) {
	next, err := wizard.RootNode.Answer("no")
	if err != nil {
		t.Fatalf("Answer(\"no\"): %v", err)
	}
	if _, err := next.Answer("anything"); !errors.Is(err, wizard.ErrNotQuestion) {
		t.Fatalf("expected ErrNotQuestion, got %v", err)
	}
}

func TestLookup_FindsEveryNodeReachableFromRoot(t *testing.T) {
	for _, key := range []string{"root", "create_default_firm", "manufacturing_coming_soon"} {
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
