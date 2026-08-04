// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package wizard implements the firm-creation wizard described in
// docs/VISION.md §3: a recursive decision tree, not a flat form. A newly
// authenticated Keycloak user with zero firm memberships (see PR 3's
// internal/identity.Memberships) is routed here instead of into the main
// app. Every feature ZonaryOS ever gains is meant to appear as a question
// on the appropriate branch of this tree (Vision §3) - this batch (Open
// Points item 12) fills in the tree's real root question sequence, in
// place of the single "do you manufacture?" placeholder slice originally
// shipped: questions that unlock features almost every commercial business
// needs come first (do you sell, do you purchase from suppliers, do you
// manage tasks/approvals), sector-specific/unusual questions come later
// (manufacturing, still a dead-end placeholder - out of scope this batch).
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
	// "manufacturing" branch, which isn't built out in this batch either).
	// Terminal, and never has a side effect.
	NodePlaceholder
)

// ActionCreateDefaultFirm is the one action this tree can reach: create a
// new firm, its default role, and seed it with whichever workflows
// SeedSelection (carried by the terminal Node reaching this action - see
// Node.Seed) says the wizard's answers actually asked for. See
// CreateDefaultFirm.
const ActionCreateDefaultFirm = "create_default_firm"

// SeedSelection is the accumulated yes/no answers from every root question
// that gates a starter workflow, carried on the terminal NodeAction that
// CreateDefaultFirm eventually reads (see Node.Seed). This is deliberately
// NOT client-supplied, round-tripped state: this wizard mechanism's HTTP
// surface (handlers.go) is stateless request/response per node, with no
// session carrying prior answers forward. Instead, the tree itself is
// built so every distinct path from RootNode to an action node is its own
// literal *Node value with its own SeedSelection baked in at tree-
// construction time (buildTree below) - the path taken to reach a
// terminal node IS the record of every answer that led there, so nothing
// needs to be threaded through the HTTP layer.
type SeedSelection struct {
	// Sells is root question 1's answer ("do you sell products or
	// services?"). Gates whether the inventory/CRM sub-questions are asked
	// at all - it seeds no workflow of its own.
	Sells bool
	// TracksInventory is root question 1's "do you track physical
	// inventory?" sub-question - only asked when Sells is true. true seeds
	// the Stock In -> Sale workflow (workflow.StockToSaleSpec).
	TracksInventory bool
	// ManagesCRM is root question 1's "do you manage customer
	// relationships?" sub-question - only asked when Sells is true. true
	// seeds the Customer Pipeline workflow (workflow.CustomerPipelineSpec).
	ManagesCRM bool
	// PurchasesFromSuppliers is root question 2. true seeds the Purchase
	// Order workflow (workflow.PurchaseOrderSpec).
	PurchasesFromSuppliers bool
	// ManagesTasks is root question 3 - asked of every firm regardless of
	// question 1's answer (tasks/approvals are universal, per the design
	// brief). true seeds the Task / Approval workflow
	// (workflow.TaskApprovalSpec).
	ManagesTasks bool
}

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
	// Seed is the accumulated SeedSelection for this specific path through
	// the tree - see SeedSelection's doc comment. Only set for a
	// NodeAction whose ActionKey is ActionCreateDefaultFirm.
	Seed *SeedSelection

	// PlaceholderKey is an i18n message key describing why this branch
	// isn't built out yet. Only set for NodePlaceholder.
	PlaceholderKey string
}

// yesNoAnswers builds the two-answer {yes, no} choice every boolean
// question in this tree uses, pointing at yesNext/noNext respectively -
// pulled out once so every question-builder below states its branching
// declaratively instead of repeating the same two-element slice literal.
func yesNoAnswers(yesNext, noNext *Node) []Answer {
	return []Answer{
		{Value: "yes", LabelKey: "answerYes", Next: yesNext},
		{Value: "no", LabelKey: "answerNo", Next: noNext},
	}
}

// buildManufactureNode is root question 4, "do you manufacture products?"
// - the tree's last question on every path. "no" reaches the
// create-default-firm action carrying sel (the full accumulated
// SeedSelection for this path); "yes" dead-ends at the pre-existing
// "coming soon" placeholder - manufacturing support itself remains out of
// scope, unchanged from the tree's original single-question slice.
func buildManufactureNode(keyPrefix string, sel SeedSelection) *Node {
	return &Node{
		Key:         keyPrefix + "_manufacture",
		Kind:        NodeQuestion,
		QuestionKey: "doYouManufacture",
		Answers: yesNoAnswers(
			&Node{
				Key:            keyPrefix + "_manufacturing_coming_soon",
				Kind:           NodePlaceholder,
				PlaceholderKey: "manufacturingComingSoon",
			},
			&Node{
				Key:       keyPrefix + "_create_default_firm",
				Kind:      NodeAction,
				ActionKey: ActionCreateDefaultFirm,
				Seed:      &sel,
			},
		),
	}
}

// buildTasksNode is root question 3, "do you manage tasks or approvals
// internally?" - asked of every firm regardless of question 1's answer
// (tasks/approvals are universal), the step right before the final
// manufacturing question on every path.
func buildTasksNode(keyPrefix string, sel SeedSelection) *Node {
	yes, no := sel, sel
	yes.ManagesTasks, no.ManagesTasks = true, false
	return &Node{
		Key:         keyPrefix + "_tasks",
		Kind:        NodeQuestion,
		QuestionKey: "doYouManageTasksOrApprovals",
		Answers: yesNoAnswers(
			buildManufactureNode(keyPrefix+"Yes", yes),
			buildManufactureNode(keyPrefix+"No", no),
		),
	}
}

// buildPurchaseNode is root question 2, "do you purchase from suppliers?"
// - reached directly whether or not question 1 ("do you sell?") was
// answered yes, since purchasing suppliers is independent of the
// sell/inventory/CRM sub-branch.
func buildPurchaseNode(keyPrefix string, sel SeedSelection) *Node {
	yes, no := sel, sel
	yes.PurchasesFromSuppliers, no.PurchasesFromSuppliers = true, false
	return &Node{
		Key:         keyPrefix + "_purchase",
		Kind:        NodeQuestion,
		QuestionKey: "doYouPurchaseFromSuppliers",
		Answers: yesNoAnswers(
			buildTasksNode(keyPrefix+"Yes", yes),
			buildTasksNode(keyPrefix+"No", no),
		),
	}
}

// buildCRMNode is root question 1's second sub-question, "do you manage
// customer relationships?" - only reached when Sells is true.
func buildCRMNode(keyPrefix string, sel SeedSelection) *Node {
	yes, no := sel, sel
	yes.ManagesCRM, no.ManagesCRM = true, false
	return &Node{
		Key:         keyPrefix + "_crm",
		Kind:        NodeQuestion,
		QuestionKey: "doYouManageCustomerRelationships",
		Answers: yesNoAnswers(
			buildPurchaseNode(keyPrefix+"Yes", yes),
			buildPurchaseNode(keyPrefix+"No", no),
		),
	}
}

// buildInventoryNode is root question 1's first sub-question, "do you
// track physical inventory?" - only reached when Sells is true.
func buildInventoryNode(keyPrefix string, sel SeedSelection) *Node {
	yes, no := sel, sel
	yes.TracksInventory, no.TracksInventory = true, false
	return &Node{
		Key:         keyPrefix + "_inventory",
		Kind:        NodeQuestion,
		QuestionKey: "doYouTrackPhysicalInventory",
		Answers: yesNoAnswers(
			buildCRMNode(keyPrefix+"Yes", yes),
			buildCRMNode(keyPrefix+"No", no),
		),
	}
}

// buildTree constructs the whole decision tree rooted at "do you sell
// products or services?" - every distinct answer path ends at its own
// literal Node carrying the SeedSelection that path accumulated (see
// SeedSelection's doc comment). "yes" opens the inventory/CRM sub-
// questions before continuing to purchasing; "no" skips straight to
// purchasing (docs/VISION.md's ordering principle: universal questions
// first, and a "no" to selling still needs purchasing/tasks/manufacturing
// asked - none of those are conditioned on selling).
func buildTree() *Node {
	sellsYes := SeedSelection{Sells: true}
	sellsNo := SeedSelection{Sells: false}
	return &Node{
		Key:         "root",
		Kind:        NodeQuestion,
		QuestionKey: "doYouSellProductsOrServices",
		Answers: yesNoAnswers(
			buildInventoryNode("sellsYes", sellsYes),
			buildPurchaseNode("sellsNo", sellsNo),
		),
	}
}

// RootNode is the tree's single entry point.
var RootNode = buildTree()

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
