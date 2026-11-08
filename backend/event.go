package alfred

import (
	"context"
	"iter"
	"time"
)

// RunEventKind identifies the semantic kind of a run event payload.
type RunEventKind string

const (
	// RunEventKindStateChanged identifies a run lifecycle transition.
	RunEventKindStateChanged RunEventKind = "run_state_changed"
	// RunEventKindMessageAdded identifies a completed transcript message.
	RunEventKindMessageAdded RunEventKind = "message_added"
	// RunEventKindToolCallRequested identifies a tool call requested by a model.
	RunEventKindToolCallRequested RunEventKind = "tool_call_requested"
	// RunEventKindToolCallCompleted identifies a completed tool invocation.
	RunEventKindToolCallCompleted RunEventKind = "tool_call_completed"
	// RunEventKindPolicyEvaluated identifies a policy decision for a tool call.
	RunEventKindPolicyEvaluated RunEventKind = "policy_evaluated"
	// RunEventKindApprovalRequested identifies a request for external approval.
	RunEventKindApprovalRequested RunEventKind = "approval_requested"
	// RunEventKindApprovalResolved identifies a resolved approval request.
	RunEventKindApprovalResolved RunEventKind = "approval_resolved"
	// RunEventKindInputRequested identifies a request for additional input.
	RunEventKindInputRequested RunEventKind = "input_requested"
	// RunEventKindInputReceived identifies a response to a request for input.
	RunEventKindInputReceived RunEventKind = "input_received"
	// RunEventKindModelCallRecorded identifies telemetry for an attempted model call.
	RunEventKindModelCallRecorded RunEventKind = "model_call_recorded"
	// RunEventKindModelCallCompleted identifies a fully consumed model stream.
	RunEventKindModelCallCompleted RunEventKind = "model_call_completed"
	// RunEventKindInterrupted identifies a recoverable execution interruption.
	RunEventKindInterrupted RunEventKind = "run_interrupted"
	// RunEventKindContextCompacted identifies a durable context checkpoint.
	RunEventKindContextCompacted RunEventKind = "context_compacted"
)

// RunEventVisibility controls whether a run event may be shown to users.
type RunEventVisibility string

const (
	// RunEventVisibilityUser marks an event as user-visible.
	RunEventVisibilityUser RunEventVisibility = "user"
	// RunEventVisibilityInternal marks an event as internal to Alfred.
	RunEventVisibilityInternal RunEventVisibility = "internal"
)

// RunEventPayload is a durable run event payload.
type RunEventPayload interface {
	Kind() RunEventKind
}

// RunEvent is an ordered fact emitted during a run. SessionID identifies its
// owning stream; Sequence is its one-based position in that durable history.
type RunEvent struct {
	ID         RunEventID
	SessionID  SessionID
	RunID      RunID
	Sequence   uint64
	OccurredAt time.Time
	Visibility RunEventVisibility
	Payload    RunEventPayload
}

// NewRunEvent contains a durable event before its identity, sequence, and time
// are assigned by the event sink.
type NewRunEvent struct {
	RunID      RunID
	Visibility RunEventVisibility
	Payload    RunEventPayload
}

// RunEventSink appends ordered durable run events.
type RunEventSink interface {
	Append(context.Context, NewRunEvent) (RunEvent, error)
}

// RunEventReader reads durable events in append order.
type RunEventReader interface {
	ReadSessionEvents(context.Context, SessionID) iter.Seq2[RunEvent, error]
	ReadRunEvents(context.Context, RunID) iter.Seq2[RunEvent, error]
}

// RunUpdateKind identifies the semantic kind of a transient run update.
type RunUpdateKind string

const (
	// RunUpdateKindContentDelta identifies incremental model output.
	RunUpdateKindContentDelta RunUpdateKind = "content_delta"
	// RunUpdateKindReasoningDelta identifies incremental readable reasoning.
	RunUpdateKindReasoningDelta RunUpdateKind = "reasoning_delta"
)

// RunUpdatePayload is a transient update emitted while a run is active.
type RunUpdatePayload interface {
	RunUpdateKind() RunUpdateKind
}

// RunContentDelta contains incremental output from one model call.
type RunContentDelta struct {
	CallID ModelCallID
	Delta  ModelContentDelta
}

// RunUpdateKind returns RunUpdateKindContentDelta.
func (RunContentDelta) RunUpdateKind() RunUpdateKind {
	return RunUpdateKindContentDelta
}

// RunReasoningDelta contains readable reasoning from one model call.
type RunReasoningDelta struct {
	CallID ModelCallID
	Delta  ModelReasoningDelta
}

// RunUpdateKind returns RunUpdateKindReasoningDelta.
func (RunReasoningDelta) RunUpdateKind() RunUpdateKind { return RunUpdateKindReasoningDelta }

// RunUpdate is a non-durable update for live session observers.
type RunUpdate struct {
	SessionID  SessionID
	RunID      RunID
	Visibility RunEventVisibility
	Payload    RunUpdatePayload
}

// RunUpdateBroadcaster publishes transient updates without affecting run execution.
type RunUpdateBroadcaster interface {
	Publish(context.Context, RunUpdate)
}

// RunStateChanged records a run lifecycle transition.
type RunStateChanged struct {
	Status RunStatus
}

// Kind returns RunEventKindStateChanged.
func (RunStateChanged) Kind() RunEventKind {
	return RunEventKindStateChanged
}

// MessageAdded records a completed user or assistant transcript message.
type MessageAdded struct {
	Message Message
	// ModelCallID identifies a transcript copy of an output committed by
	// ModelCallCompleted. It is empty for invocation messages and older records.
	ModelCallID ModelCallID
}

// Kind returns RunEventKindMessageAdded.
func (MessageAdded) Kind() RunEventKind {
	return RunEventKindMessageAdded
}

// ToolCallRequested records a tool call requested by a model.
type ToolCallRequested struct {
	ModelCallID ModelCallID
	Call        ToolCall
}

// Kind returns RunEventKindToolCallRequested.
func (ToolCallRequested) Kind() RunEventKind {
	return RunEventKindToolCallRequested
}

// ToolCallCompleted records the model-visible outcome of a tool invocation.
type ToolCallCompleted struct {
	Result ToolResult
}

// Kind returns RunEventKindToolCallCompleted.
func (ToolCallCompleted) Kind() RunEventKind {
	return RunEventKindToolCallCompleted
}

// PolicyEvaluated records the policy decision for a tool call.
type PolicyEvaluated struct {
	CallID   ToolCallID
	Decision PolicyDecision
}

// Kind returns RunEventKindPolicyEvaluated.
func (PolicyEvaluated) Kind() RunEventKind {
	return RunEventKindPolicyEvaluated
}

// ApprovalRequest asks for approval to execute an exact tool action.
type ApprovalRequest struct {
	ID           ApprovalID
	CallID       ToolCallID
	ActionDigest string
	Reason       string
}

// ApprovalRequested records a request for external approval.
type ApprovalRequested struct {
	Request ApprovalRequest
}

// Kind returns RunEventKindApprovalRequested.
func (ApprovalRequested) Kind() RunEventKind {
	return RunEventKindApprovalRequested
}

// ApprovalOutcome describes how an approval request was resolved.
type ApprovalOutcome string

const (
	// ApprovalOutcomeApproved permits the requested action.
	ApprovalOutcomeApproved ApprovalOutcome = "approved"
	// ApprovalOutcomeDenied rejects the requested action.
	ApprovalOutcomeDenied ApprovalOutcome = "denied"
)

// ApprovalResolution resolves an approval request.
type ApprovalResolution struct {
	ApprovalID ApprovalID
	Outcome    ApprovalOutcome
	Reason     string
}

// ApprovalResolved records the resolution of an approval request.
type ApprovalResolved struct {
	Resolution ApprovalResolution
}

// Kind returns RunEventKindApprovalResolved.
func (ApprovalResolved) Kind() RunEventKind {
	return RunEventKindApprovalResolved
}

// InputRequest asks for additional input during a run. A zero CallID indicates
// that the request did not originate from a tool call.
type InputRequest struct {
	ID     InputRequestID
	CallID ToolCallID
	Prompt []ContentPart
}

// InputRequested records a request for additional input.
type InputRequested struct {
	Request InputRequest
}

// Kind returns RunEventKindInputRequested.
func (InputRequested) Kind() RunEventKind {
	return RunEventKindInputRequested
}

// InputResponse responds to a request for additional input.
type InputResponse struct {
	RequestID InputRequestID
	Content   []ContentPart
}

// InputReceived records a response to a request for additional input.
type InputReceived struct {
	Response InputResponse
}

// Kind returns RunEventKindInputReceived.
func (InputReceived) Kind() RunEventKind {
	return RunEventKindInputReceived
}

// ModelCallRecorded records usage, cost, and timing for an attempted model call.
type ModelCallRecorded struct {
	Metrics ModelCallMetrics
}

// Kind returns RunEventKindModelCallRecorded.
func (ModelCallRecorded) Kind() RunEventKind {
	return RunEventKindModelCallRecorded
}

// ModelCallCompleted commits outputs from a successfully consumed model stream.
type ModelCallCompleted struct {
	CallID ModelCallID
	// Output commits complete messages, reasoning, and tool calls in provider output order.
	// Nil identifies older checkpoints that committed tool-request events only.
	Output []ModelInput
}

// Kind returns RunEventKindModelCallCompleted.
func (ModelCallCompleted) Kind() RunEventKind {
	return RunEventKindModelCallCompleted
}

// RunInterrupted records a recoverable failure at the last durable checkpoint.
// Reason is the code of the boundary-defined error kind that caused it.
type RunInterrupted struct {
	Reason  string
	Message string
}

// Kind returns RunEventKindInterrupted.
func (RunInterrupted) Kind() RunEventKind {
	return RunEventKindInterrupted
}

// ContextCompacted records a summary of an inclusive range of durable events.
type ContextCompacted struct {
	FromEventID    RunEventID
	ThroughEventID RunEventID
	Summary        []ContentPart
	ModelCallID    ModelCallID
}

// Kind returns RunEventKindContextCompacted.
func (ContextCompacted) Kind() RunEventKind {
	return RunEventKindContextCompacted
}
