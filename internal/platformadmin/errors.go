// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package platformadmin

import "errors"

// ErrNotPlatformAdmin is returned by ListFirms when the caller's email is
// not on the Allowlist - the sole rejection reason this package has, since
// there is no partial/tiered platform-admin access in this batch.
var ErrNotPlatformAdmin = errors.New("caller is not a platform admin")
