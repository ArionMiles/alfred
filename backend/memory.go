package alfred

import (
	"context"
	"time"
)

// MemoryScopeKind identifies the boundary within which a memory is available.
type MemoryScopeKind string

const (
	// MemoryScopeUser makes a memory available across a user's projects.
	MemoryScopeUser MemoryScopeKind = "user"
	// MemoryScopeProject limits a memory to one project.
	MemoryScopeProject MemoryScopeKind = "project"
	// MemoryScopeSession limits a memory to one session.
	MemoryScopeSession MemoryScopeKind = "session"
)

// MemoryScope describes the user, project, or session boundary of a memory.
type MemoryScope struct {
	Kind      MemoryScopeKind
	UserID    UserID
	ProjectID ProjectID
	SessionID SessionID
}

// MemoryRecord is selected durable knowledge available for later retrieval.
type MemoryRecord struct {
	ID        MemoryID
	Scope     MemoryScope
	Content   []ContentPart
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MemoryQuery searches one or more memory scopes using multimodal content.
type MemoryQuery struct {
	Scopes  []MemoryScope
	Content []ContentPart
	Limit   uint32
}

// MemoryMatch is one search result. Results are ordered by decreasing
// relevance; the absolute score is implementation-defined.
type MemoryMatch struct {
	Record    MemoryRecord
	Relevance float64
}

// Memory searches, saves, and forgets durable memory records.
type Memory interface {
	Search(context.Context, MemoryQuery) ([]MemoryMatch, error)
	Save(context.Context, MemoryRecord) error
	Forget(context.Context, MemoryID) error
}
