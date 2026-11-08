package alfred

// IDGenerator creates strongly typed identifiers.
type IDGenerator[T ~string] interface {
	New() (T, error)
}

// InvocationID identifies an invocation.
type InvocationID string

// ProjectID is a generated, opaque ID that is safe as one path component.
type ProjectID string

// SessionID is a generated, opaque ID that is safe as one path component.
type SessionID string

// RunID identifies a run.
type RunID string

// RunEventID identifies a run event.
type RunEventID string

// ToolID identifies a tool in Alfred's global tool registry.
type ToolID string

// ToolCallID identifies a tool call.
type ToolCallID string

// ProviderID identifies a configured model provider instance.
type ProviderID string

// ModelID identifies a model within a provider.
type ModelID string

// ModelCallID identifies one inference call to a model provider.
type ModelCallID string

// ApprovalID identifies an approval request.
type ApprovalID string

// InputRequestID identifies a request for additional input.
type InputRequestID string

// UserID identifies a user whose memories and future authenticated actions are scoped.
type UserID string

// MemoryID identifies a durable memory record.
type MemoryID string

// SkillName identifies a skill by its Agent Skills name.
type SkillName string
