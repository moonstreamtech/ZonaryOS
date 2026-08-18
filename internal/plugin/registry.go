// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package plugin

// Registry holds every compiled-in plugin's own registered extension
// points - WorkflowHook, KPIProvider, and ReportSource, per this batch's
// own design brief. Built once in cmd/server/main.go and populated by
// each enabled plugin's own registration call (e.g.
// activitylog.Register(registry) - see internal/plugins/activitylog's
// own doc comment) BEFORE any HTTP route is registered or any request is
// served - Part 2's own "immutable after init, no hot-reloading" scope
// boundary means Registry has no mutex: every Register* call happens
// during that single-threaded startup sequence, and every Dispatch*/
// accessor call happens only after startup has finished appending, so
// there is no concurrent read-while-write to guard against in practice.
type Registry struct {
	workflowHooks []WorkflowHook
	kpiProviders  []KPIProvider
	reportSources []ReportSource
}

// NewRegistry returns an empty Registry ready for RegisterWorkflowHook/
// RegisterKPIProvider/RegisterReportSource calls.
func NewRegistry() *Registry {
	return &Registry{}
}

// RegisterWorkflowHook adds hook to the registry - cmd/server/main.go
// reads it back via WorkflowHooks() to bridge it into
// internal/workflow.RegisterTransitionHook, the function that actually
// dispatches to it after every committed transition (see that function's
// own doc comment for why the real dispatch list lives in
// internal/workflow rather than here - the same import-cycle reasoning
// RegisterKPIProvider/RegisterReportSource below document for their own
// internal/reports counterparts).
func (r *Registry) RegisterWorkflowHook(hook WorkflowHook) {
	r.workflowHooks = append(r.workflowHooks, hook)
}

// RegisterKPIProvider adds provider to the registry - cmd/server/main.go
// reads it back via KPIProviders() to bridge it into
// internal/reports.RegisterExternalKPIProvider (see that function's own
// doc comment for why the bridging happens in main.go rather than this
// package calling into internal/reports directly - an import-cycle
// concern, not a design preference).
func (r *Registry) RegisterKPIProvider(provider KPIProvider) {
	r.kpiProviders = append(r.kpiProviders, provider)
}

// RegisterReportSource adds source to the registry - same
// main.go-bridges-it-into-internal/reports shape as RegisterKPIProvider
// above, via ReportSources() and internal/reports.RegisterExternalSource.
func (r *Registry) RegisterReportSource(source ReportSource) {
	r.reportSources = append(r.reportSources, source)
}

// WorkflowHooks returns every registered WorkflowHook, in registration
// order - read-only accessor for cmd/server/main.go's own bridging loop
// (bridging into internal/workflow.RegisterTransitionHook - see
// internal/workflow/plugin_hooks.go's own doc comment for why that
// bridging happens in main.go rather than this package calling into
// internal/workflow directly).
func (r *Registry) WorkflowHooks() []WorkflowHook {
	return r.workflowHooks
}

// KPIProviders returns every registered KPIProvider, in registration
// order - read-only accessor for cmd/server/main.go's own bridging loop.
func (r *Registry) KPIProviders() []KPIProvider {
	return r.kpiProviders
}

// ReportSources returns every registered ReportSource, in registration
// order - read-only accessor for cmd/server/main.go's own bridging loop.
func (r *Registry) ReportSources() []ReportSource {
	return r.reportSources
}
