package sqlite

import (
	"testing"
	"time"

	modernsqlite "modernc.org/sqlite"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestRunStoreLifecycle(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	project := createTestProject(t, store, "project-1")
	session := createTestSession(t, store, project.ID, "session-1")
	otherSession := createTestSession(t, store, project.ID, "session-2")
	createdAt := time.Date(2026, time.October, 25, 11, 0, 0, 0, time.UTC)
	firstInvocation := createTestInvocation(
		t,
		store,
		alfred.Invocation{
			ID: "invocation-1", ProjectID: project.ID, SessionID: session.ID, CreatedAt: createdAt,
		},
	)
	secondInvocation := createTestInvocation(
		t,
		store,
		alfred.Invocation{
			ID: "invocation-2", ProjectID: project.ID, SessionID: session.ID,
			CreatedAt: createdAt.Add(time.Second),
		},
	)
	otherInvocation := createTestInvocation(
		t,
		store,
		alfred.Invocation{
			ID: "invocation-3", ProjectID: project.ID, SessionID: otherSession.ID, CreatedAt: createdAt,
		},
	)
	first := alfred.Run{
		ID: "run-1", InvocationID: firstInvocation.ID, SessionID: session.ID,
		DefaultModel: alfred.ModelRef{ProviderID: "provider-1", ModelID: "model-1"},
		Status:       alfred.RunStatusQueued,
		CreatedAt:    createdAt,
	}
	second := alfred.Run{
		ID: "run-2", InvocationID: secondInvocation.ID, SessionID: session.ID,
		DefaultModel: alfred.ModelRef{ProviderID: "provider-2", ModelID: "model-2"},
		Status:       alfred.RunStatusQueued,
		CreatedAt:    createdAt,
	}
	other := alfred.Run{
		ID: "run-3", InvocationID: otherInvocation.ID, SessionID: otherSession.ID,
		Status: alfred.RunStatusQueued, CreatedAt: createdAt,
	}

	for _, run := range []alfred.Run{second, first, other} {
		if err := store.CreateRun(t.Context(), run); err != nil {
			t.Fatalf("CreateRun(%q) error = %v", run.ID, err)
		}
	}
	if err := store.TransitionRun(
		t.Context(),
		first.ID,
		alfred.RunStatusQueued,
		alfred.RunStatusRunning,
	); err != nil {
		t.Fatalf("TransitionRun() error = %v", err)
	}

	got, err := store.GetRun(t.Context(), first.ID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	want := first
	want.Status = alfred.RunStatusRunning
	if got != want {
		t.Fatalf("GetRun() = %#v, want %#v", got, want)
	}

	firstPage, err := store.ListRuns(t.Context(), session.ID, alfred.RunListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("ListRuns() first page error = %v", err)
	}
	if len(firstPage.Runs) != 1 || firstPage.Runs[0].ID != first.ID || firstPage.Next == nil {
		t.Fatalf("ListRuns() first page = %#v, want first run and next cursor", firstPage)
	}
	secondPage, err := store.ListRuns(t.Context(), session.ID, alfred.RunListOptions{
		Limit: 1,
		After: firstPage.Next,
	})
	if err != nil {
		t.Fatalf("ListRuns() second page error = %v", err)
	}
	if len(secondPage.Runs) != 1 || secondPage.Runs[0].ID != second.ID || secondPage.Next != nil {
		t.Fatalf("ListRuns() second page = %#v, want final second run", secondPage)
	}
}

func TestCreateRunMapsConstraintErrors(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	project := createTestProject(t, store, "project-1")
	session := createTestSession(t, store, project.ID, "session-1")
	otherSession := createTestSession(t, store, project.ID, "session-2")
	invocation := createTestInvocation(
		t,
		store,
		alfred.Invocation{ID: "invocation-1", ProjectID: project.ID, SessionID: session.ID},
	)
	run := alfred.Run{
		ID: "run-1", InvocationID: invocation.ID, SessionID: session.ID,
		Status: alfred.RunStatusQueued,
	}
	if err := store.CreateRun(t.Context(), run); err != nil {
		t.Fatalf("CreateRun() first error = %v", err)
	}

	err := store.CreateRun(t.Context(), run)
	if errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("CreateRun() duplicate error = %v, want conflict", err)
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("CreateRun() duplicate error = %v, want wrapped *sqlite.Error", err)
	}

	mismatchedInvocation := createTestInvocation(
		t,
		store,
		alfred.Invocation{ID: "invocation-2", ProjectID: project.ID, SessionID: session.ID},
	)
	run.ID = "run-2"
	run.InvocationID = mismatchedInvocation.ID
	run.SessionID = otherSession.ID
	err = store.CreateRun(t.Context(), run)
	if errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("CreateRun() session mismatch error = %v, want not found", err)
	}
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("CreateRun() session mismatch error = %v, want wrapped *sqlite.Error", err)
	}
}

func TestRunStoreMapsMissingAndStaleTransitions(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)

	if _, err := store.GetRun(t.Context(), "missing"); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("GetRun() error = %v, want not found", err)
	}
	if err := store.TransitionRun(
		t.Context(),
		"missing",
		alfred.RunStatusQueued,
		alfred.RunStatusRunning,
	); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("TransitionRun() missing error = %v, want not found", err)
	}

	project := createTestProject(t, store, "project-1")
	session := createTestSession(t, store, project.ID, "session-1")
	invocation := createTestInvocation(
		t,
		store,
		alfred.Invocation{ID: "invocation-1", ProjectID: project.ID, SessionID: session.ID},
	)
	run := alfred.Run{
		ID: "run-1", InvocationID: invocation.ID, SessionID: session.ID,
		Status: alfred.RunStatusRunning,
	}
	if err := store.CreateRun(t.Context(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if err := store.TransitionRun(
		t.Context(),
		run.ID,
		alfred.RunStatusQueued,
		alfred.RunStatusCompleted,
	); errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("TransitionRun() stale error = %v, want conflict", err)
	}
	got, err := store.GetRun(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("GetRun() after stale transition error = %v", err)
	}
	if got.Status != alfred.RunStatusRunning {
		t.Fatalf("GetRun() status = %q, want %q", got.Status, alfred.RunStatusRunning)
	}
}

func createTestInvocation(
	t *testing.T,
	store *Store,
	invocation alfred.Invocation,
) alfred.Invocation {
	t.Helper()
	if err := store.CreateInvocation(t.Context(), invocation); err != nil {
		t.Fatalf("CreateInvocation(%q) error = %v", invocation.ID, err)
	}
	return invocation
}
