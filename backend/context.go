package alfred

import "context"

// ContextRequest identifies the run and selected model for which context is
// being assembled.
type ContextRequest struct {
	ProjectID    ProjectID
	SessionID    SessionID
	RunID        RunID
	Invocation   Invocation
	Model        ModelInfo
	SkillCatalog SkillCatalog
}

// ModelContext is the initial context assembled for a run. Input includes prior
// session context and the current invocation, excluding current-run continuation
// events, which the runner replays separately.
type ModelContext struct {
	Instructions string
	Input        []ModelInput
	Tools        []ToolDefinition
}

// ContextBuilder assembles initial model context from Alfred's durable and
// harness-owned context sources. Rebuilding for the same run must retain the
// same history boundary so run replay does not duplicate current-run input.
type ContextBuilder interface {
	Build(context.Context, ContextRequest) (ModelContext, error)
}
