// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package wizard implements the firm-creation wizard described in
// docs/VISION.md §3: a recursive decision tree, not a flat form. A newly
// authenticated Keycloak user with zero firm memberships (see PR 3's
// internal/identity.Memberships) is routed here instead of into the main
// app. Every feature ZonaryOS ever gains is meant to appear as a question
// on the appropriate branch of this tree (Vision §3) - this slice
// populates exactly one root question ("do you manufacture?"), but the
// tree/node mechanism itself is the real deliverable: a second question
// nests in as another Node without restructuring anything here.
package wizard

import "fmt"

// NodeKind distinguishes the three kinds of node the tree can hold.
type NodeKind int

const (
	// NodeQuestion presents Answers for the caller to choose from.
	NodeQuestion NodeKind = iota
	// NodeAction has no answers of its own - reaching it means an
	// ActionKey-identified side effect should run (e.g. creating the
	// firm). Terminal.
	NodeAction
	// NodePlaceholder is a dead end with nothing to do yet (e.g. the
	// "manufacturing" branch, which isn't built out in this slice).
	// Terminal, and never has a side effect.
	NodePlaceholder
)

// ActionCreateDefaultFirm is the one action this slice's tree can reach:
// create a new firm, its default role, and seed it with the Stock In ->
// Sale workflow. See CreateDefaultFirm.
const ActionCreateDefaultFirm = "create_default_firm"

// Answer is one option a NodeQuestion offers. Value is the stable
// identifier a client submits back (see handlers.go); LabelKey is an
// i18n message key under the "Wizard" namespace (web/messages/*.json) -
// never literal text, per CLAUDE.md Never-Violate Rule 4.
type Answer struct {
	Value    string
	LabelKey string
	Next     *Node
}

// Node is one node in the wizard's decision tree.
type Node struct {
	// Key uniquely identifies this node across the whole tree - the path
	// segment clients address it by (see handlers.go).
	Key  string
	Kind NodeKind

	// QuestionKey is an i18n message key for the question text. Only set
	// for NodeQuestion.
	QuestionKey string
	// Answers are the choices available at a NodeQuestion. Only set for
	// NodeQuestion.
	Answers []Answer

	// ActionKey identifies which side effect to run on reaching this
	// node. Only set for NodeAction.
	ActionKey string

	// PlaceholderKey is an i18n message key describing why this branch
	// isn't built out yet. Only set for NodePlaceholder.
	PlaceholderKey string
}

// RootNode is the tree's single entry point for this slice: "do you
// manufacture?" -> "no" creates a default (non-manufacturing) firm;
// "yes" dead-ends at a "coming soon" placeholder, deliberately not a real
// manufacturing flow (out of scope - see the PR description).
var RootNode = &Node{
	Key:         "root",
	Kind:        NodeQuestion,
	QuestionKey: "doYouManufacture",
	Answers: []Answer{
		{
			Value:    "no",
			LabelKey: "answerNo",
			Next: &Node{
				Key:       "create_default_firm",
				Kind:      NodeAction,
				ActionKey: ActionCreateDefaultFirm,
			},
		},
		{
			Value:    "yes",
			LabelKey: "answerYes",
			Next: &Node{
				Key:            "manufacturing_coming_soon",
				Kind:           NodePlaceholder,
				PlaceholderKey: "manufacturingComingSoon",
			},
		},
	},
}

// nodesByKey indexes every node reachable from RootNode, built once at
// package init so handlers.go can resolve a client-supplied node key
// without walking the tree from scratch on every request.
var nodesByKey = indexTree(RootNode)

func indexTree(root *Node) map[string]*Node {
	index := make(map[string]*Node)
	var visit func(n *Node)
	visit = func(n *Node) {
		if n == nil {
			return
		}
		index[n.Key] = n
		for _, a := range n.Answers {
			visit(a.Next)
		}
	}
	visit(root)
	return index
}

// ErrNodeNotFound means no node in the tree has the given key.
var ErrNodeNotFound = fmt.Errorf("wizard node not found")

// ErrNotQuestion means the node exists but isn't a NodeQuestion, so it
// has no answers to submit against.
var ErrNotQuestion = fmt.Errorf("wizard node is not a question")

// ErrUnknownAnswer means the submitted answer value doesn't match any of
// the question node's Answers.
var ErrUnknownAnswer = fmt.Errorf("unknown answer for this wizard node")

// Lookup resolves a node by its Key.
func Lookup(key string) (*Node, error) {
	n, ok := nodesByKey[key]
	if !ok {
		return nil, ErrNodeNotFound
	}
	return n, nil
}

// Answer resolves the next node reached by submitting answerValue at
// node, which must be a NodeQuestion.
func (n *Node) Answer(answerValue string) (*Node, error) {
	if n.Kind != NodeQuestion {
		return nil, ErrNotQuestion
	}
	for _, a := range n.Answers {
		if a.Value == answerValue {
			return a.Next, nil
		}
	}
	return nil, ErrUnknownAnswer
}
