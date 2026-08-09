// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package version holds the single build-time version string every
// process in this repository reports (today, only GET /health - see
// internal/health). No release/tagging process exists yet (Open Points
// item 34's rollback-trigger gap), so this defaults to "dev" and is
// overridden at build time via
// -ldflags "-X github.com/moonstreamtech/ZonaryOS/internal/platform/version.Version=<value>"
// (e.g. a git SHA or tag) when a real build pipeline sets one - see
// docs/DEVELOPMENT.md.
package version

// Version is deliberately a package-level var, not a const: -ldflags -X
// can only override a var.
var Version = "dev"
