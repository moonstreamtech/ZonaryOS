// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package license

// Module key constants. This is a small, honest, deliberately
// provisional stand-in for Open Points item 30's real module/pricing
// catalog, which remains fully open (no band thresholds, unit prices, or
// module-to-base-fee mapping exist yet). These three names were picked
// because they name real, already-built things in this codebase today -
// not invented modules that don't exist:
//
//   - ModuleWorkflowEngine: internal/workflow, the generic workflow
//     engine every firm's stock-to-sale/customer-pipeline flows run on.
//   - ModuleAuditTrail: internal/auditlog, the per-firm audit log.
//   - ModulePlatformAdmin: internal/platformadmin, the cross-firm,
//     allowlist-gated metadata view - the one module this batch actually
//     wires a gate onto (see that package's ListFirms).
//
// Only ModulePlatformAdmin is wired into a real Allow/IsModuleActive call
// today (Task's explicit instruction: prove the mechanism on one narrow,
// low-blast-radius path, not roll it out everywhere). The other two
// constants exist so this list is honest about the small set of real
// modules this codebase has, not because internal/workflow or
// internal/auditlog call into this package yet - they don't.
const (
	ModuleWorkflowEngine = "workflow_engine"
	ModuleAuditTrail     = "audit_trail"
	ModulePlatformAdmin  = "platform_admin"
)
