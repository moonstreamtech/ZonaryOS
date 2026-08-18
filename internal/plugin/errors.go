// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package plugin

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - enabling/configuring a plugin for
	// a firm is owner-gated, the same tier as
	// internal/localization.CreateTaxRate.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's plugin configuration")

	// ErrPluginNotFound means no plugins row with the given id exists in
	// the platform-wide catalog.
	ErrPluginNotFound = errors.New("plugin not found")

	// ErrInvalidPlugin means CreatePlugin/UpdatePlugin was given a
	// structurally invalid plugin (empty name/version/entryPoint).
	ErrInvalidPlugin = errors.New("invalid plugin")

	// ErrFirmPluginConfigNotFound means no firm_plugin_configs row exists
	// for the given (firmID, pluginID) pair.
	ErrFirmPluginConfigNotFound = errors.New("firm plugin configuration not found")
)
