// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package openapi embeds openapi.yaml so it ships inside the single
// ZonaryOS binary (same "embed, don't read off disk at request time"
// reasoning migrations.FS already establishes for the SQL migration
// files) - internal/apidocs.RegisterRoutes serves it verbatim at
// GET /api/docs. Lives here, not under internal/, so this file sits next
// to the openapi.yaml it embeds - go:embed directives cannot reference a
// path outside their own package directory.
package openapi

import "embed"

//go:embed openapi.yaml
var FS embed.FS
