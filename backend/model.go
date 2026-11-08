package alfred

import (
	"context"
	"encoding/json"
	"iter"
	"time"
)

// ModelRef identifies a model and the configured provider that serves it.
type ModelRef struct {
	ProviderID ProviderID
	ModelID    ModelID
}

// ModelCapabilities describes the inputs, outputs, and behavior a model supports.
type ModelCapabilities struct {
	InputModalities  []ContentKind
	OutputModalities []ContentKind
	ContextWindow    uint64
	MaxOutputTokens  uint64
	ToolCalling      bool
	Reasoning        bool
}

// ModelInfo describes a model exposed by a configured provider.
type ModelInfo struct {
	Ref          ModelRef
	Name         string
	Capabilities ModelCapabilities
}

// ModelInputKind identifies an item supplied to a model.
type ModelInputKind string

const (
	// ModelInputKindMessage identifies a user or assistant message.
	ModelInputKindMessage ModelInputKind = "message"
	// ModelInputKindToolCall identifies a tool call requested by a model.
	ModelInputKindToolCall ModelInputKind = "tool_call"
	// ModelInputKindToolResult identifies the result of a tool call.
	ModelInputKindToolResult ModelInputKind = "tool_result"
	// ModelInputKindReasoning identifies reasoning retained for continuation.
	ModelInputKindReasoning ModelInputKind = "reasoning"
)

// ModelInputPayload is the semantic content of one model input item.
type ModelInputPayload interface {
	ModelInputKind() ModelInputKind
}

// NativeData retains adapter-owned JSON under adapter-defined, versioned keys.
// The core preserves it without interpreting it or exposing it to clients.
type NativeData map[string]json.RawMessage

// ModelInput keeps semantic content and its native replay data together.
type ModelInput struct {
	Payload ModelInputPayload
	Native  NativeData
}

// MessageRole identifies the author of a message supplied to a model.
type MessageRole string

const (
	// MessageRoleUser identifies a user-authored message.
	MessageRoleUser MessageRole = "user"
	// MessageRoleAssistant identifies a model-authored message.
	MessageRoleAssistant MessageRole = "assistant"
)

// Message contains multimodal user or assistant content.
type Message struct {
	Role    MessageRole
	Content []ContentPart
}

// ModelInputKind returns ModelInputKindMessage.
func (Message) ModelInputKind() ModelInputKind {
	return ModelInputKindMessage
}

// ReasoningTextKind distinguishes provider-exposed reasoning from a summary.
type ReasoningTextKind string

const (
	// ReasoningTextReasoning identifies provider-exposed reasoning text.
	ReasoningTextReasoning ReasoningTextKind = "reasoning"
	// ReasoningTextSummary identifies a provider-exposed reasoning summary.
	ReasoningTextSummary ReasoningTextKind = "summary"
)

// Reasoning is the readable portion of a completed reasoning item. Text may
// be empty when its model envelope carries only native replay data.
type Reasoning struct {
	Text     string
	TextKind ReasoningTextKind
}

// ModelInputKind returns ModelInputKindReasoning.
func (Reasoning) ModelInputKind() ModelInputKind {
	return ModelInputKindReasoning
}

// ModelRequest is the bounded input for one model inference call.
type ModelRequest struct {
	ID           ModelCallID
	RunID        RunID
	Model        ModelRef
	Instructions string
	Input        []ModelInput
	Tools        []ToolDefinition
}

// ModelEventKind identifies an event emitted by a model provider.
type ModelEventKind string

const (
	// ModelEventKindContentDelta identifies an incremental content update.
	ModelEventKindContentDelta ModelEventKind = "content_delta"
	// ModelEventKindReasoningDelta identifies incremental readable reasoning.
	ModelEventKindReasoningDelta ModelEventKind = "reasoning_delta"
	// ModelEventKindMessage identifies a complete assistant message.
	ModelEventKindMessage ModelEventKind = "message"
	// ModelEventKindToolCall identifies a complete tool call requested by a model.
	ModelEventKindToolCall ModelEventKind = "tool_call"
	// ModelEventKindReasoning identifies one complete reasoning item.
	ModelEventKindReasoning ModelEventKind = "reasoning"
	// ModelEventKindUsage identifies token usage reported by a provider.
	ModelEventKindUsage ModelEventKind = "usage"
	// ModelEventKindCompleted identifies successful completion of a model stream.
	ModelEventKindCompleted ModelEventKind = "completed"
)

// ModelEventPayload is a payload emitted by a model provider.
type ModelEventPayload interface {
	ModelEventKind() ModelEventKind
}

// ModelEvent is one event in a model provider's response stream.
type ModelEvent struct {
	CallID  ModelCallID
	Payload ModelEventPayload
	// Native accompanies completed messages, reasoning, and tool calls only.
	Native NativeData
}

// ModelContentDelta is an incremental update to one output content part.
type ModelContentDelta struct {
	Index uint32
	Delta ContentPart
}

// ModelEventKind returns ModelEventKindContentDelta.
func (ModelContentDelta) ModelEventKind() ModelEventKind {
	return ModelEventKindContentDelta
}

// ModelReasoningDelta contains readable reasoning from one output part.
type ModelReasoningDelta struct {
	Index    uint32
	Text     string
	TextKind ReasoningTextKind
}

// ModelEventKind returns ModelEventKindReasoningDelta.
func (ModelReasoningDelta) ModelEventKind() ModelEventKind { return ModelEventKindReasoningDelta }

// ModelMessage contains one complete assistant message. Providers emit completed
// messages, reasoning, and tool calls in output order; deltas are only for live presentation.
type ModelMessage struct {
	Message Message
}

// ModelEventKind returns ModelEventKindMessage.
func (ModelMessage) ModelEventKind() ModelEventKind {
	return ModelEventKindMessage
}

// ModelToolCall contains a complete tool call requested by a model.
type ModelToolCall struct {
	Call ToolCall
}

// ModelEventKind returns ModelEventKindToolCall.
func (ModelToolCall) ModelEventKind() ModelEventKind {
	return ModelEventKindToolCall
}

// ModelReasoning carries a completed reasoning item in provider output order.
type ModelReasoning struct {
	Reasoning Reasoning
}

// ModelEventKind returns ModelEventKindReasoning.
func (ModelReasoning) ModelEventKind() ModelEventKind {
	return ModelEventKindReasoning
}

// TokenUsage is normalized token accounting for one model call. Cached input
// and reasoning tokens remain part of the input and output totals respectively.
type TokenUsage struct {
	InputTokens       uint64
	CachedInputTokens uint64
	OutputTokens      uint64
	ReasoningTokens   uint64
	Additional        map[string]uint64
}

// ModelCost is the monetary cost attributed to one model call, expressed in
// millionths of Currency.
type ModelCost struct {
	Currency     string
	InputMicros  uint64
	OutputMicros uint64
	TotalMicros  uint64
	PricingID    string
	Estimated    bool
}

// ModelCallTiming records wall-clock bounds and monotonic durations for one
// model call. A nil first-event or first-output duration means that milestone
// was not observed.
type ModelCallTiming struct {
	DispatchedAt      time.Time
	CompletedAt       time.Time
	Duration          time.Duration
	TimeToFirstEvent  *time.Duration
	TimeToFirstOutput *time.Duration
}

// ModelCallOutcome describes how a model call ended.
type ModelCallOutcome string

const (
	// ModelCallOutcomeCompleted indicates successful stream completion.
	ModelCallOutcomeCompleted ModelCallOutcome = "completed"
	// ModelCallOutcomeFailed indicates that the provider call failed.
	ModelCallOutcomeFailed ModelCallOutcome = "failed"
	// ModelCallOutcomeCancelled indicates that the provider call was cancelled.
	ModelCallOutcomeCancelled ModelCallOutcome = "cancelled"
)

// ModelCallMetrics contains the usage, cost, and timing attributed to one model call.
type ModelCallMetrics struct {
	CallID  ModelCallID
	Model   ModelRef
	Outcome ModelCallOutcome
	Usage   TokenUsage
	Cost    ModelCost
	Timing  ModelCallTiming
}

// ModelUsage contains token usage reported by a provider.
type ModelUsage struct {
	Tokens TokenUsage
}

// ModelEventKind returns ModelEventKindUsage.
func (ModelUsage) ModelEventKind() ModelEventKind {
	return ModelEventKindUsage
}

// ModelFinishReason describes why a model finished producing output.
type ModelFinishReason string

const (
	// ModelFinishReasonStop indicates a natural stop.
	ModelFinishReasonStop ModelFinishReason = "stop"
	// ModelFinishReasonToolCall indicates that the model requested a tool call.
	ModelFinishReasonToolCall ModelFinishReason = "tool_call"
	// ModelFinishReasonLength indicates that the model reached an output limit.
	ModelFinishReasonLength ModelFinishReason = "length"
	// ModelFinishReasonContentFilter indicates that a content filter stopped output.
	ModelFinishReasonContentFilter ModelFinishReason = "content_filter"
	// ModelFinishReasonOther identifies an unrecognized provider finish reason.
	ModelFinishReasonOther ModelFinishReason = "other"
)

// ModelCompleted reports successful completion of a model event stream.
type ModelCompleted struct {
	Reason         ModelFinishReason
	ProviderReason string
}

// ModelEventKind returns ModelEventKindCompleted.
func (ModelCompleted) ModelEventKind() ModelEventKind {
	return ModelEventKindCompleted
}

// ModelProvider lists models and streams inference events from one configured
// provider instance. Completed output items are emitted in their original order.
// Text deltas must be followed by their complete ModelMessage. A successful
// stream emits exactly one ModelCompleted event after all completed output items.
type ModelProvider interface {
	ID() ProviderID
	Models(context.Context) ([]ModelInfo, error)
	ResolveModel(context.Context, ModelID) (ModelInfo, error)
	Stream(context.Context, ModelRequest) iter.Seq2[ModelEvent, error]
}

// ProviderResolver resolves a configured provider by identity.
type ProviderResolver interface {
	ResolveProvider(context.Context, ProviderID) (ModelProvider, error)
}
