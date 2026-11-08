package alfred

import (
	"context"
	"time"
)

// ProjectCursor identifies a position in project creation order.
type ProjectCursor struct {
	CreatedAt time.Time
	ID        ProjectID
}

// ProjectListOptions controls one page of projects.
type ProjectListOptions struct {
	Limit uint32
	After *ProjectCursor
}

// ProjectPage is one page of projects and the cursor for the following page.
type ProjectPage struct {
	Projects []Project
	Next     *ProjectCursor
}

// SessionCursor identifies a position in session creation order.
type SessionCursor struct {
	CreatedAt time.Time
	ID        SessionID
}

// SessionListOptions controls one page of sessions.
type SessionListOptions struct {
	Limit uint32
	After *SessionCursor
}

// SessionPage is one page of sessions and the cursor for the following page.
type SessionPage struct {
	Sessions []Session
	Next     *SessionCursor
}

// RunCursor identifies a position in run creation order.
type RunCursor struct {
	CreatedAt time.Time
	ID        RunID
}

// RunListOptions controls one page of runs.
type RunListOptions struct {
	Limit uint32
	After *RunCursor
}

// RunPage is one page of runs and the cursor for the following page.
type RunPage struct {
	Runs []Run
	Next *RunCursor
}

// ProjectStore persists project metadata.
type ProjectStore interface {
	CreateProject(context.Context, Project) error
	GetProject(context.Context, ProjectID) (Project, error)
	GetProjectByRoot(context.Context, string) (Project, error)
	ListProjects(context.Context, ProjectListOptions) (ProjectPage, error)
	RenameProject(context.Context, ProjectID, string) error
	SetProjectSkillTrust(context.Context, ProjectID, ProjectSkillTrust) error
}

// SessionStore persists session metadata.
type SessionStore interface {
	CreateSession(context.Context, Session) error
	GetSession(context.Context, SessionID) (Session, error)
	ListSessions(context.Context, ProjectID, SessionListOptions) (SessionPage, error)
	SetSessionTitle(context.Context, SessionID, string) error
}

// InvocationStore persists immutable invocations.
type InvocationStore interface {
	CreateInvocation(context.Context, Invocation) error
	GetInvocation(context.Context, InvocationID) (Invocation, error)
}

// RunStore persists run metadata and lifecycle state. TransitionRun changes a
// status only when the stored status matches expected.
type RunStore interface {
	CreateRun(context.Context, Run) error
	GetRun(context.Context, RunID) (Run, error)
	ListRuns(context.Context, SessionID, RunListOptions) (RunPage, error)
	TransitionRun(context.Context, RunID, RunStatus, RunStatus) error
}

// ProviderInstanceStore persists service-managed provider configuration.
// Config-owned instances are not stored here. Credentials remain separate.
type ProviderInstanceStore interface {
	// PutProviderInstance creates or replaces the complete instance metadata.
	PutProviderInstance(context.Context, ProviderInstance) error
	// GetProviderInstance returns an instance, or a NotFound error.
	GetProviderInstance(context.Context, ProviderID) (ProviderInstance, error)
	// ListProviderInstances returns all saved instances, including disabled ones,
	// ordered by ID so startup and clients see a stable collection.
	ListProviderInstances(context.Context) ([]ProviderInstance, error)
	// DeleteProviderInstance is idempotent and does not delete credentials.
	DeleteProviderInstance(context.Context, ProviderID) error
}

// ModelSelectionStore persists the service-wide default for new runs.
// Existing runs retain their own immutable DefaultModel.
type ModelSelectionStore interface {
	// GetSelectedModel returns NotFound when no model has been selected.
	GetSelectedModel(context.Context) (ModelRef, error)
	SetSelectedModel(context.Context, ModelRef) error
}
