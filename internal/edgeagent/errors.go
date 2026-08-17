// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package edgeagent

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - registering an agent or issuing a
	// token is owner-gated (see this package's doc comment).
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's edge agents")

	// ErrAgentNotFound means no edge_agents row with the given id is
	// visible in the caller's firm context.
	ErrAgentNotFound = errors.New("edge agent not found")

	// ErrInvalidAgent means CreateAgent was given a structurally invalid
	// agent (e.g. an empty name).
	ErrInvalidAgent = errors.New("invalid edge agent")

	// ErrInvalidToken means the presented bearer token does not match
	// any live (non-expired) edge_agent_tokens row.
	ErrInvalidToken = errors.New("invalid or expired edge agent token")

	// ErrInvalidCommand means CreateCommand was given a structurally
	// invalid command (e.g. an empty commandType).
	ErrInvalidCommand = errors.New("invalid edge agent command")

	// ErrCommandNotFound means no edge_agent_commands row with the given
	// id is visible in the caller's firm/agent context.
	ErrCommandNotFound = errors.New("edge agent command not found")
)
