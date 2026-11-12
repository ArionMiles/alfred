package alfred

import (
	"context"
	"time"
)

// InvocationSource describes what caused an invocation.
type InvocationSource string

const (
	// InvocationSourceUser identifies an invocation submitted by a user through a client.
	InvocationSourceUser InvocationSource = "user"
	// InvocationSourceSchedule identifies an invocation produced by a schedule.
	InvocationSourceSchedule InvocationSource = "schedule"
	// InvocationSourceWebhook identifies an invocation produced by a webhook.
	InvocationSourceWebhook InvocationSource = "webhook"
	// InvocationSourceMonitor identifies an invocation produced by a monitor.
	InvocationSourceMonitor InvocationSource = "monitor"
	// InvocationSourceInternal identifies an invocation produced by Alfred itself.
	InvocationSourceInternal InvocationSource = "internal"
)

// Invocation is an immutable request for Alfred to perform work. A zero
// SessionID asks Alfred to create a new session in the target project.
type Invocation struct {
	ID        InvocationID
	ProjectID ProjectID
	SessionID SessionID
	Source    InvocationSource
	Input     []ContentPart
	CreatedAt time.Time
}

// InvocationRequest asks Alfred to create and dispatch a run. A zero SessionID
// asks Alfred to create a new session in the target project.
type InvocationRequest struct {
	ProjectID ProjectID
	SessionID SessionID
	Source    InvocationSource
	Input     []ContentPart
}

// ProjectSkillTrust records whether project-provided skill instructions may be
// loaded by Alfred.
type ProjectSkillTrust string

const (
	// ProjectSkillTrustUnknown indicates that the user has not made a decision.
	ProjectSkillTrustUnknown ProjectSkillTrust = "unknown"
	// ProjectSkillTrustTrusted permits skills from the project's skill roots.
	ProjectSkillTrustTrusted ProjectSkillTrust = "trusted"
	// ProjectSkillTrustDenied excludes skills from the project's skill roots.
	ProjectSkillTrustDenied ProjectSkillTrust = "denied"
)

// Project is a durable workspace and context boundary.
type Project struct {
	ID         ProjectID
	Name       string
	Root       string
	SkillTrust ProjectSkillTrust
	CreatedAt  time.Time
}

// ProjectResolver resolves an existing local directory to its durable project,
// creating the project at the supplied time when the directory has not been seen before.
type ProjectResolver interface {
	ResolveProject(context.Context, string, time.Time) (Project, error)
}

// Session is a durable context thread within a project.
type Session struct {
	ID        SessionID
	ProjectID ProjectID
	Title     string
	CreatedAt time.Time
}

// RunStatus describes the lifecycle state of a run.
type RunStatus string

const (
	// RunStatusQueued indicates that a run is waiting to start.
	RunStatusQueued RunStatus = "queued"
	// RunStatusRunning indicates that a run is executing.
	RunStatusRunning RunStatus = "running"
	// RunStatusWaitingForApproval indicates that a run needs an approval decision.
	RunStatusWaitingForApproval RunStatus = "waiting_for_approval"
	// RunStatusWaitingForInput indicates that a run needs additional input.
	RunStatusWaitingForInput RunStatus = "waiting_for_input"
	// RunStatusInterrupted indicates that a recoverable external condition
	// stopped execution at a durable point.
	RunStatusInterrupted RunStatus = "interrupted"
	// RunStatusCompleted indicates that a run finished successfully.
	RunStatusCompleted RunStatus = "completed"
	// RunStatusFailed indicates that a run ended with an error.
	RunStatusFailed RunStatus = "failed"
	// RunStatusCancelled indicates that a run was cancelled.
	RunStatusCancelled RunStatus = "cancelled"
)

// Run is one execution of the agent loop caused by an invocation.
type Run struct {
	ID           RunID
	InvocationID InvocationID
	SessionID    SessionID
	DefaultModel ModelRef
	Status       RunStatus
	CreatedAt    time.Time
}

// AgentRunner starts or resumes a durable run and continues until the run
// reaches a terminal state or waits for external input.
type AgentRunner interface {
	Execute(context.Context, RunID) error
}

// RunDispatcher accepts durable runs for execution owned by the background
// service. Dispatch returns after the run has been accepted, not after it ends.
type RunDispatcher interface {
	Dispatch(context.Context, RunID) error
	// Shutdown stops admission, cancels executions, and settles their durable state.
	// Invocation sources must finish their in-flight control requests before calling it.
	Shutdown(context.Context) error
}

// InvocationService creates and dispatches runs using the supplied creation time.
type InvocationService interface {
	Invoke(context.Context, InvocationRequest, time.Time) (Run, error)
}

// RunControl supplies external decisions and cancellation to existing runs.
type RunControl interface {
	ResolveApproval(context.Context, RunID, ApprovalResolution) error
	ProvideInput(context.Context, RunID, InputResponse) error
	ResumeRun(context.Context, RunID) error
	CancelRun(context.Context, RunID) error
}
