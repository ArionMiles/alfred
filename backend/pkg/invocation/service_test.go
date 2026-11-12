package invocation

import (
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestInvokeCreatesSessionAndDispatchesQueuedRun(t *testing.T) {
	createdAt := time.Date(2026, time.October, 24, 10, 30, 0, 0, time.UTC)
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	service, dependencies := newTestService(t, dispatcher)
	dependencies.projects.EXPECT().GetProject(gomock.Any(), alfred.ProjectID("project-1")).Return(
		alfred.Project{ID: "project-1"},
		nil,
	)
	var createdSession alfred.Session
	dependencies.sessions.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		session alfred.Session,
	) error {
		createdSession = session
		return nil
	})
	var createdInvocation alfred.Invocation
	dependencies.invocations.EXPECT().CreateInvocation(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		invocation alfred.Invocation,
	) error {
		createdInvocation = invocation
		return nil
	})
	var createdRun alfred.Run
	dependencies.runs.EXPECT().CreateRun(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		run alfred.Run,
	) error {
		createdRun = run
		return nil
	})
	var createdEvent alfred.NewRunEvent
	dependencies.events.EXPECT().Append(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		event alfred.NewRunEvent,
	) (alfred.RunEvent, error) {
		createdEvent = event
		return alfred.RunEvent{}, nil
	})
	dispatcher.EXPECT().Dispatch(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id alfred.RunID) error {
		if id == "" || id != createdRun.ID {
			t.Fatalf("dispatched run %q, persisted %q", id, createdRun.ID)
		}
		return nil
	})
	input := []alfred.ContentPart{alfred.TextContent{Text: "hello"}}

	run, err := service.Invoke(t.Context(), alfred.InvocationRequest{
		ProjectID: "project-1",
		Source:    alfred.InvocationSourceUser,
		Input:     input,
	}, createdAt)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if createdSession.ID == "" || createdSession.ProjectID != "project-1" || createdSession.CreatedAt != createdAt {
		t.Fatalf("CreateSession() session = %#v", createdSession)
	}
	if createdInvocation.ID == "" ||
		createdInvocation.SessionID != createdSession.ID ||
		createdInvocation.CreatedAt != createdAt {
		t.Fatalf("CreateInvocation() invocation = %#v", createdInvocation)
	}
	wantRun := alfred.Run{
		ID:           createdRun.ID,
		InvocationID: createdInvocation.ID,
		SessionID:    createdSession.ID,
		DefaultModel: alfred.ModelRef{ProviderID: "provider-1", ModelID: "model-1"},
		Status:       alfred.RunStatusQueued,
		CreatedAt:    createdAt,
	}
	if run.ID == "" || run != wantRun || createdRun != wantRun {
		t.Fatalf("Invoke() run = %#v, persisted = %#v, want %#v", run, createdRun, wantRun)
	}
	message, ok := createdEvent.Payload.(alfred.MessageAdded)
	if !ok || createdEvent.RunID != run.ID || createdEvent.Visibility != alfred.RunEventVisibilityUser ||
		message.Message.Role != alfred.MessageRoleUser ||
		message.Message.Content[0].(alfred.TextContent).Text != "hello" {
		t.Fatalf("transcript event = %#v", createdEvent)
	}
	input[0] = alfred.TextContent{Text: "changed"}
	if got := createdInvocation.Input[0].(alfred.TextContent).Text; got != "hello" {
		t.Fatalf("persisted input = %q after request mutation, want hello", got)
	}
	if got := message.Message.Content[0].(alfred.TextContent).Text; got != "hello" {
		t.Fatalf("transcript input = %q after request mutation, want hello", got)
	}
}

func TestInvokeReusesSessionInRequestedProject(t *testing.T) {
	createdAt := time.Now()
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	service, dependencies := newTestService(t, dispatcher)
	dependencies.projects.EXPECT().GetProject(gomock.Any(), alfred.ProjectID("project-1")).Return(
		alfred.Project{ID: "project-1"},
		nil,
	)
	session := alfred.Session{ID: "session-existing", ProjectID: "project-1"}
	dependencies.sessions.EXPECT().GetSession(gomock.Any(), session.ID).Return(session, nil)
	dependencies.invocations.EXPECT().CreateInvocation(gomock.Any(), gomock.Any()).Return(nil)
	dependencies.runs.EXPECT().CreateRun(gomock.Any(), gomock.Any()).Return(nil)
	dependencies.events.EXPECT().Append(gomock.Any(), gomock.Any()).Return(alfred.RunEvent{}, nil)
	dispatcher.EXPECT().Dispatch(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id alfred.RunID) error {
		if id == "" {
			t.Fatal("dispatched an empty run ID")
		}
		return nil
	})

	run, err := service.Invoke(t.Context(), alfred.InvocationRequest{
		ProjectID: "project-1",
		SessionID: session.ID,
		Source:    alfred.InvocationSourceSchedule,
	}, createdAt)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if run.SessionID != session.ID {
		t.Fatalf("Invoke() session ID = %q, want %q", run.SessionID, session.ID)
	}
}

func TestInvokeRejectsSessionFromAnotherProject(t *testing.T) {
	createdAt := time.Now()
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	service, dependencies := newTestService(t, dispatcher)
	dependencies.projects.EXPECT().GetProject(gomock.Any(), alfred.ProjectID("project-1")).Return(
		alfred.Project{ID: "project-1"},
		nil,
	)
	dependencies.sessions.EXPECT().GetSession(gomock.Any(), alfred.SessionID("session-1")).Return(
		alfred.Session{ID: "session-1", ProjectID: "project-2"},
		nil,
	)

	_, err := service.Invoke(t.Context(), alfred.InvocationRequest{
		ProjectID: "project-1",
		SessionID: "session-1",
	}, createdAt)
	if got := errors.WhatKind(err); got != errors.InvalidArgument {
		t.Fatalf("errors.WhatKind() = %#v, want %#v", got, errors.InvalidArgument)
	}
}

func TestInvokePersistsBeforeDispatch(t *testing.T) {
	createdAt := time.Now()
	storeErr := errors.B.Text("write failed").Build()
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	service, dependencies := newTestService(t, dispatcher)
	expectNewSessionInvocation(t, dependencies)
	dependencies.runs.EXPECT().CreateRun(gomock.Any(), gomock.Any()).Return(storeErr)

	_, err := service.Invoke(t.Context(), alfred.InvocationRequest{ProjectID: "project-1"}, createdAt)
	if !errors.Is(err, storeErr) {
		t.Fatalf("Invoke() error = %v, want wrapped store error", err)
	}
}

func TestInvokeReturnsOnlyErrorWhenDispatchFails(t *testing.T) {
	createdAt := time.Now()
	dispatchErr := errors.B.Text("dispatch failed").Build()
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	service, dependencies := newTestService(t, dispatcher)
	expectNewSessionInvocation(t, dependencies)
	dependencies.runs.EXPECT().CreateRun(gomock.Any(), gomock.Any()).Return(nil)
	dependencies.events.EXPECT().Append(gomock.Any(), gomock.Any()).Return(alfred.RunEvent{}, nil)
	dispatcher.EXPECT().Dispatch(gomock.Any(), gomock.Any()).Return(dispatchErr)

	run, err := service.Invoke(t.Context(), alfred.InvocationRequest{ProjectID: "project-1"}, createdAt)
	if !errors.Is(err, dispatchErr) {
		t.Fatalf("Invoke() error = %v, want wrapped dispatch error", err)
	}
	if run != (alfred.Run{}) {
		t.Fatalf("Invoke() run = %#v, want zero value", run)
	}
}

func TestInvokeDoesNotDispatchWhenTranscriptAppendFails(t *testing.T) {
	createdAt := time.Now()
	appendErr := errors.B.Text("append failed").Build()
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	service, dependencies := newTestService(t, dispatcher)
	expectNewSessionInvocation(t, dependencies)
	dependencies.runs.EXPECT().CreateRun(gomock.Any(), gomock.Any()).Return(nil)
	dependencies.events.EXPECT().Append(gomock.Any(), gomock.Any()).Return(alfred.RunEvent{}, appendErr)

	_, err := service.Invoke(t.Context(), alfred.InvocationRequest{ProjectID: "project-1"}, createdAt)
	if !errors.Is(err, appendErr) {
		t.Fatalf("Invoke() error = %v, want wrapped append error", err)
	}
}

type testDependencies struct {
	projects    *mocks.MockProjectStore
	sessions    *mocks.MockSessionStore
	invocations *mocks.MockInvocationStore
	runs        *mocks.MockRunStore
	events      *mocks.MockRunEventSink
}

func newTestService(
	t *testing.T,
	dispatcher alfred.RunDispatcher,
) (*Service, testDependencies) {
	t.Helper()
	controller := gomock.NewController(t)
	dependencies := testDependencies{
		projects:    mocks.NewMockProjectStore(controller),
		sessions:    mocks.NewMockSessionStore(controller),
		invocations: mocks.NewMockInvocationStore(controller),
		runs:        mocks.NewMockRunStore(controller),
		events:      mocks.NewMockRunEventSink(controller),
	}
	selection := mocks.NewMockModelSelectionStore(controller)
	selection.EXPECT().GetSelectedModel(gomock.Any()).Return(alfred.ModelRef{ProviderID: "provider-1", ModelID: "model-1"}, nil).AnyTimes()
	return NewService(Dependencies{
		ModelSelection: selection,
		Projects:       dependencies.projects,
		Sessions:       dependencies.sessions,
		Invocations:    dependencies.invocations,
		Runs:           dependencies.runs,

		Dispatcher: dispatcher,
		Events:     dependencies.events,
	}), dependencies
}

func expectNewSessionInvocation(t *testing.T, dependencies testDependencies) {
	t.Helper()
	dependencies.projects.EXPECT().GetProject(gomock.Any(), alfred.ProjectID("project-1")).Return(
		alfred.Project{ID: "project-1"},
		nil,
	)
	dependencies.sessions.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil)
	dependencies.invocations.EXPECT().CreateInvocation(gomock.Any(), gomock.Any()).Return(nil)
}

func TestInvokeWithoutSelectedModelDoesNotCreateSessionOrRun(t *testing.T) {
	const op = "TestInvokeWithoutSelectedModelDoesNotCreateSessionOrRun"

	t.Parallel()
	controller := gomock.NewController(t)
	projects := mocks.NewMockProjectStore(controller)
	projects.EXPECT().GetProject(gomock.Any(), alfred.ProjectID("project-1")).Return(alfred.Project{ID: "project-1"}, nil)
	selection := mocks.NewMockModelSelectionStore(controller)
	selection.EXPECT().GetSelectedModel(gomock.Any()).Return(alfred.ModelRef{}, errors.B.Op(op).KindNotFound().Build())
	service := NewService(Dependencies{
		Projects: projects, ModelSelection: selection,
		Sessions: mocks.NewMockSessionStore(controller), Invocations: mocks.NewMockInvocationStore(controller),
		Runs: mocks.NewMockRunStore(controller), Dispatcher: mocks.NewMockRunDispatcher(controller),
		Events: mocks.NewMockRunEventSink(controller),
	})
	run, err := service.Invoke(t.Context(), alfred.InvocationRequest{ProjectID: "project-1"}, time.Time{})
	if run.ID != "" || errors.WhatKind(err) != errors.FailedPrecondition || errors.UserMsg(err) != "Choose a model before sending a message." {
		t.Fatalf("run=%v error=%v", run, err)
	}
}
