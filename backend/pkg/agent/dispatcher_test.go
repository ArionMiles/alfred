package agent

import (
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func newTestDispatcher(t *testing.T, runner *Runner) *Dispatcher {
	t.Helper()
	dispatcher := NewDispatcher(runner)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := dispatcher.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return dispatcher
}

func TestDispatcherShutdownInterruptsExecutionAndRejectsNewWork(t *testing.T) {
	t.Parallel()
	model := testModel()
	run := testRun(model.Ref)
	fixture := newRunnerFixture(t, run, model)
	fixture.provider.started = make(chan struct{})
	fixture.provider.waitForCancellation = true
	dispatcher := newTestDispatcher(t, fixture.runner())
	if err := dispatcher.Dispatch(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	<-fixture.provider.started
	if err := dispatcher.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := fixture.runs.status(); got != alfred.RunStatusInterrupted {
		t.Fatalf("status = %s, want interrupted", got)
	}
	var interrupted bool
	for _, event := range fixture.events.snapshot() {
		if payload, ok := event.Payload.(alfred.RunInterrupted); ok {
			interrupted = payload.Reason == errors.Unavailable.Code && payload.Message != ""
		}
	}
	if !interrupted {
		t.Fatal("shutdown did not record a resumable interruption")
	}
	for name, operation := range map[string]func() error{
		"dispatch": func() error { return dispatcher.Dispatch(t.Context(), run.ID) },
		"resume":   func() error { return dispatcher.ResumeRun(t.Context(), run.ID) },
		"cancel":   func() error { return dispatcher.CancelRun(t.Context(), run.ID) },
		"approval": func() error { return dispatcher.ResolveApproval(t.Context(), run.ID, alfred.ApprovalResolution{}) },
		"input":    func() error { return dispatcher.ProvideInput(t.Context(), run.ID, alfred.InputResponse{}) },
	} {
		if err := operation(); errors.WhatKind(err) != errors.FailedPrecondition {
			t.Errorf("%s after shutdown = %v", name, err)
		}
	}
}

func TestDispatcherShutdownWaitsForWriterAndCanRetry(t *testing.T) {
	t.Parallel()
	model := testModel()
	run := testRun(model.Ref)
	fixture := newRunnerFixture(t, run, model)
	started := make(chan struct{})
	released := make(chan struct{})
	contexts := mocks.NewMockContextBuilder(fixture.controller)
	contexts.EXPECT().Build(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ alfred.ContextRequest) (alfred.ModelContext, error) {
		close(started)
		<-released
		return alfred.ModelContext{}, ctx.Err()
	})
	fixture.dependencies.Contexts = contexts
	dispatcher := newTestDispatcher(t, fixture.runner())
	if err := dispatcher.Dispatch(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := dispatcher.Shutdown(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown = %v, want canceled", err)
	}
	if got := fixture.runs.status(); got != alfred.RunStatusQueued {
		t.Fatalf("status before writer stops = %s", got)
	}
	close(released)
	if err := dispatcher.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := fixture.runs.status(); got != alfred.RunStatusInterrupted {
		t.Fatalf("status after retry = %s", got)
	}
}

func TestDispatcherShutdownPreservesDurablePauseAndTerminalStates(t *testing.T) {
	t.Parallel()
	for _, status := range []alfred.RunStatus{alfred.RunStatusWaitingForInput, alfred.RunStatusWaitingForApproval, alfred.RunStatusInterrupted, alfred.RunStatusCompleted, alfred.RunStatusFailed, alfred.RunStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			run := testRun(testModel().Ref)
			run.Status = status
			fixture := newRunnerFixture(t, run, testModel())
			dispatcher := newTestDispatcher(t, fixture.runner())
			if err := dispatcher.interruptStoppedRun(t.Context(), run.ID); err != nil {
				t.Fatal(err)
			}
			if got := fixture.runs.status(); got != status {
				t.Fatalf("status = %s, want %s", got, status)
			}
			if events := fixture.events.snapshot(); len(events) != 0 {
				t.Fatalf("unexpected events: %v", events)
			}
		})
	}
}
