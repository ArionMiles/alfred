// Package invocation creates and dispatches Alfred invocations.
package invocation

import (
	"context"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type projectReader interface {
	GetProject(context.Context, alfred.ProjectID) (alfred.Project, error)
}

type sessionStore interface {
	GetSession(context.Context, alfred.SessionID) (alfred.Session, error)
	CreateSession(context.Context, alfred.Session) error
}

type invocationWriter interface {
	CreateInvocation(context.Context, alfred.Invocation) error
}

type runWriter interface {
	CreateRun(context.Context, alfred.Run) error
}

type modelSelectionReader interface {
	GetSelectedModel(context.Context) (alfred.ModelRef, error)
}

// Dependencies contains the harness-owned components used by a Service.
type Dependencies struct {
	ModelSelection        modelSelectionReader
	Projects              projectReader
	Sessions              sessionStore
	Invocations           invocationWriter
	Runs                  runWriter
	InvocationIDGenerator alfred.IDGenerator[alfred.InvocationID]
	SessionIDGenerator    alfred.IDGenerator[alfred.SessionID]
	RunIDGenerator        alfred.IDGenerator[alfred.RunID]
	Clock                 alfred.Clock
	Dispatcher            alfred.RunDispatcher
	Events                alfred.RunEventSink
}

// Service creates durable invocation records before dispatching their runs.
type Service struct {
	projects              projectReader
	sessions              sessionStore
	invocations           invocationWriter
	runs                  runWriter
	modelSelection        modelSelectionReader
	invocationIDGenerator alfred.IDGenerator[alfred.InvocationID]
	sessionIDGenerator    alfred.IDGenerator[alfred.SessionID]
	runIDGenerator        alfred.IDGenerator[alfred.RunID]
	clock                 alfred.Clock
	dispatcher            alfred.RunDispatcher
	events                alfred.RunEventSink
}

// NewService creates an invocation service with a persisted model selection reader.
func NewService(dependencies Dependencies) *Service {
	return &Service{
		projects:              dependencies.Projects,
		sessions:              dependencies.Sessions,
		invocations:           dependencies.Invocations,
		runs:                  dependencies.Runs,
		modelSelection:        dependencies.ModelSelection,
		invocationIDGenerator: dependencies.InvocationIDGenerator,
		sessionIDGenerator:    dependencies.SessionIDGenerator,
		runIDGenerator:        dependencies.RunIDGenerator,
		clock:                 dependencies.Clock,
		dispatcher:            dependencies.Dispatcher,
		events:                dependencies.Events,
	}
}

// Invoke creates the invocation, its queued run, and a session when needed,
// then dispatches the durable run.
func (service *Service) Invoke(
	ctx context.Context,
	request alfred.InvocationRequest,
) (alfred.Run, error) {
	const op = "Invoke"

	if _, err := service.projects.GetProject(ctx, request.ProjectID); err != nil {
		return alfred.Run{}, errors.B.
			Op(op).
			Textf("get project %q", request.ProjectID).
			Err(err).
			Build()
	}

	model, err := service.modelSelection.GetSelectedModel(ctx)
	if err != nil {
		builder := errors.B.Op(op).Err(err)
		if errors.WhatKind(err) == errors.NotFound {
			builder = builder.KindFailedPrecondition().UserMsg("Choose a model before sending a message.")
		}
		return alfred.Run{}, builder.Build()
	}
	createdAt := service.clock.Now()
	session, err := service.getOrCreateSession(
		ctx,
		request.ProjectID,
		request.SessionID,
		createdAt,
	)
	if err != nil {
		return alfred.Run{}, errors.B.
			Op(op).
			Text("resolve invocation session").
			Err(err).
			Build()
	}

	invocationID, err := service.invocationIDGenerator.New()
	if err != nil {
		return alfred.Run{}, errors.B.Op(op).Text("generate invocation ID").Err(err).Build()
	}
	runID, err := service.runIDGenerator.New()
	if err != nil {
		return alfred.Run{}, errors.B.Op(op).Text("generate run ID").Err(err).Build()
	}

	invocation := alfred.Invocation{
		ID:        invocationID,
		ProjectID: request.ProjectID,
		SessionID: session.ID,
		Source:    request.Source,
		Input:     append([]alfred.ContentPart(nil), request.Input...),
		CreatedAt: createdAt,
	}
	run := alfred.Run{
		ID:           runID,
		InvocationID: invocationID,
		SessionID:    session.ID,
		DefaultModel: model,
		Status:       alfred.RunStatusQueued,
		CreatedAt:    createdAt,
	}

	if err := service.invocations.CreateInvocation(ctx, invocation); err != nil {
		return alfred.Run{}, errors.B.
			Op(op).
			Textf("create invocation %q", invocation.ID).
			Err(err).
			Build()
	}
	if err := service.runs.CreateRun(ctx, run); err != nil {
		return alfred.Run{}, errors.B.Op(op).Textf("create run %q", run.ID).Err(err).Build()
	}
	if _, err := service.events.Append(ctx, alfred.NewRunEvent{
		RunID:      run.ID,
		Visibility: alfred.RunEventVisibilityUser,
		Payload: alfred.MessageAdded{Message: alfred.Message{
			Role:    alfred.MessageRoleUser,
			Content: append([]alfred.ContentPart(nil), request.Input...),
		}},
	}); err != nil {
		return alfred.Run{}, errors.B.
			Op(op).
			Textf("record input for invocation %q", invocation.ID).
			Err(err).
			Build()
	}
	if err := service.dispatcher.Dispatch(ctx, run.ID); err != nil {
		return alfred.Run{}, errors.B.Op(op).Textf("dispatch run %q", run.ID).Err(err).Build()
	}
	return run, nil
}

func (service *Service) getOrCreateSession(
	ctx context.Context,
	projectID alfred.ProjectID,
	sessionID alfred.SessionID,
	createdAt time.Time,
) (alfred.Session, error) {
	const op = "getOrCreateSession"

	if sessionID == "" {
		id, err := service.sessionIDGenerator.New()
		if err != nil {
			return alfred.Session{}, errors.B.Op(op).Text("generate session ID").Err(err).Build()
		}
		session := alfred.Session{
			ID:        id,
			ProjectID: projectID,
			CreatedAt: createdAt,
		}
		if err := service.sessions.CreateSession(ctx, session); err != nil {
			return alfred.Session{}, errors.B.
				Op(op).
				Textf("create session %q", session.ID).
				Err(err).
				Build()
		}
		return session, nil
	}

	session, err := service.sessions.GetSession(ctx, sessionID)
	if err != nil {
		return alfred.Session{}, errors.B.
			Op(op).
			Textf("get session %q", sessionID).
			Err(err).
			Build()
	}
	if session.ProjectID != projectID {
		return alfred.Session{}, errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("session %q belongs to project %q, not %q", sessionID, session.ProjectID, projectID).
			UserMsg("the session does not belong to the requested project").
			Build()
	}
	return session, nil
}

var _ alfred.InvocationService = (*Service)(nil)
