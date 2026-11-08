package alfred

import (
	"context"
	"encoding/json"
)

// ToolDefinition describes a tool to Alfred and model providers.
type ToolDefinition struct {
	ID          ToolID
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolCall is an immutable request to invoke a tool during a run.
type ToolCall struct {
	ID        ToolCallID
	RunID     RunID
	ToolID    ToolID
	Arguments json.RawMessage
}

// ModelInputKind returns ModelInputKindToolCall.
func (ToolCall) ModelInputKind() ModelInputKind {
	return ModelInputKindToolCall
}

// ToolResult is the model-visible outcome of a tool call. IsError reports a
// failure produced by the tool rather than a failure of the execution boundary.
type ToolResult struct {
	CallID  ToolCallID
	Output  []ContentPart
	IsError bool
}

// ModelInputKind returns ModelInputKindToolResult.
func (ToolResult) ModelInputKind() ModelInputKind {
	return ModelInputKindToolResult
}

// Tool describes and invokes one tool implementation. Agent runs invoke tools
// through an Executor rather than calling this interface directly.
type Tool interface {
	Definition() ToolDefinition
	Invoke(context.Context, ToolInvocation) (ToolOutcome, error)
}

// ToolInvocation supplies trusted execution context separately from model arguments.
type ToolInvocation struct {
	Call        ToolCall
	ProjectRoot string
}

// ToolOutcome either completes a call or asks the user for input before resuming it.
type ToolOutcome struct {
	Result      *ToolResult
	InputPrompt []ContentPart
}

// ToolResolver resolves a registered tool by identity.
type ToolResolver interface {
	ResolveTool(context.Context, ToolID) (Tool, error)
}

// ToolCatalog lists definitions for registered tools.
type ToolCatalog interface {
	ListTools(context.Context) ([]ToolDefinition, error)
}

// ApprovalGrant identifies prior approval for the exact action represented by
// ActionDigest. The policy manager validates the grant's provenance.
type ApprovalGrant struct {
	ApprovalID   ApprovalID
	ActionDigest string
}

// ExecutionRequest supplies a tool call and the project and session in which it
// would execute. Approval is present only when resuming a previously approved
// action.
type ExecutionRequest struct {
	ProjectID ProjectID
	SessionID SessionID
	Call      ToolCall
	Approval  *ApprovalGrant
}

// ExecutionOutcome contains the policy decision and, when allowed, the tool
// result. ActionDigest binds an approval to the exact evaluated action.
type ExecutionOutcome struct {
	Decision     PolicyDecision
	ActionDigest string
	Result       *ToolResult
	InputPrompt  []ContentPart
}

// Executor invokes tool calls through Alfred's execution middleware chain.
type Executor interface {
	Execute(context.Context, ExecutionRequest) (ExecutionOutcome, error)
}

// PolicyOutcome describes the result of evaluating an action against policy.
type PolicyOutcome string

const (
	// PolicyOutcomeAllow permits the action.
	PolicyOutcomeAllow PolicyOutcome = "allow"
	// PolicyOutcomeDeny rejects the action.
	PolicyOutcomeDeny PolicyOutcome = "deny"
	// PolicyOutcomeAsk requires an external approval decision.
	PolicyOutcomeAsk PolicyOutcome = "ask"
)

// PolicyDecision is the result of a policy evaluation.
type PolicyDecision struct {
	Outcome PolicyOutcome
	Reason  string
	RuleID  string
}

// AuthorizationRequest describes the exact tool action to evaluate against
// policy and the project and session in which it would run.
type AuthorizationRequest struct {
	ProjectID ProjectID
	SessionID SessionID
	Tool      ToolDefinition
	Call      ToolCall
	Approval  *ApprovalGrant
}

// PolicyManager evaluates whether an exact tool action may execute.
type PolicyManager interface {
	Evaluate(context.Context, AuthorizationRequest) (PolicyDecision, error)
}
