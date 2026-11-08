package sqlite

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	modernsqlite "modernc.org/sqlite"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestInvocationStoreLifecycle(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	project := createTestProject(t, store, "project-1")
	session := createTestSession(t, store, project.ID, "session-1")
	invocation := alfred.Invocation{
		ID:        "invocation-1",
		ProjectID: project.ID,
		SessionID: session.ID,
		Source:    alfred.InvocationSourceUser,
		Input: []alfred.ContentPart{
			alfred.TextContent{Text: "look at this"},
			alfred.ResourceContent{
				Type: alfred.ContentKindImage,
				Resource: alfred.ResourceRef{
					URI:       "file:///image.png",
					MediaType: "image/png",
					Name:      "image.png",
				},
			},
		},
		CreatedAt: time.Date(2026, time.October, 25, 10, 0, 0, 123, time.UTC),
	}

	if err := store.CreateInvocation(t.Context(), invocation); err != nil {
		t.Fatalf("CreateInvocation() error = %v", err)
	}
	got, err := store.GetInvocation(t.Context(), invocation.ID)
	if err != nil {
		t.Fatalf("GetInvocation() error = %v", err)
	}
	if !reflect.DeepEqual(got, invocation) {
		t.Fatalf("GetInvocation() = %#v, want %#v", got, invocation)
	}
}

func TestInvocationStorePreservesNilAndEmptyInput(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	project := createTestProject(t, store, "project-1")
	session := createTestSession(t, store, project.ID, "session-1")

	for _, invocation := range []alfred.Invocation{
		{ID: "nil-input", ProjectID: project.ID, SessionID: session.ID},
		{ID: "empty-input", ProjectID: project.ID, SessionID: session.ID, Input: []alfred.ContentPart{}},
	} {
		if err := store.CreateInvocation(t.Context(), invocation); err != nil {
			t.Fatalf("CreateInvocation(%q) error = %v", invocation.ID, err)
		}
		got, err := store.GetInvocation(t.Context(), invocation.ID)
		if err != nil {
			t.Fatalf("GetInvocation(%q) error = %v", invocation.ID, err)
		}
		if !reflect.DeepEqual(got.Input, invocation.Input) {
			t.Fatalf("GetInvocation(%q) input = %#v, want %#v", invocation.ID, got.Input, invocation.Input)
		}
	}
}

func TestCreateInvocationMapsConstraintErrors(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	project := createTestProject(t, store, "project-1")
	otherProject := createTestProject(t, store, "project-2")
	session := createTestSession(t, store, project.ID, "session-1")
	invocation := alfred.Invocation{
		ID: "invocation-1", ProjectID: project.ID, SessionID: session.ID,
	}
	if err := store.CreateInvocation(t.Context(), invocation); err != nil {
		t.Fatalf("CreateInvocation() first error = %v", err)
	}

	err := store.CreateInvocation(t.Context(), invocation)
	if errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("CreateInvocation() duplicate error = %v, want conflict", err)
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("CreateInvocation() duplicate error = %v, want wrapped *sqlite.Error", err)
	}

	invocation.ID = "project-mismatch"
	invocation.ProjectID = otherProject.ID
	err = store.CreateInvocation(t.Context(), invocation)
	if errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("CreateInvocation() project mismatch error = %v, want not found", err)
	}
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("CreateInvocation() project mismatch error = %v, want wrapped *sqlite.Error", err)
	}
}

func TestInvocationStoreRejectsInvalidContent(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	project := createTestProject(t, store, "project-1")
	session := createTestSession(t, store, project.ID, "session-1")
	invocation := alfred.Invocation{
		ID: "invocation-1", ProjectID: project.ID, SessionID: session.ID,
		Input: []alfred.ContentPart{unsupportedContent{}},
	}

	err := store.CreateInvocation(t.Context(), invocation)
	if errors.WhatKind(err) != errors.InvalidInput {
		t.Fatalf("CreateInvocation() error = %v, want invalid input", err)
	}
}

func TestGetInvocationMapsMissingAndInvalidRecords(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)

	if _, err := store.GetInvocation(t.Context(), "missing"); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("GetInvocation() missing error = %v, want not found", err)
	}

	project := createTestProject(t, store, "project-1")
	session := createTestSession(t, store, project.ID, "session-1")
	invocation := alfred.Invocation{ID: "invocation-1", ProjectID: project.ID, SessionID: session.ID}
	if err := store.CreateInvocation(t.Context(), invocation); err != nil {
		t.Fatalf("CreateInvocation() error = %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `
		UPDATE invocations SET input_json = @input_json WHERE id = @id
	`, sql.Named("input_json", `{}`), sql.Named("id", invocation.ID)); err != nil {
		t.Fatalf("corrupt invocation input: %v", err)
	}
	if _, err := store.GetInvocation(t.Context(), invocation.ID); errors.WhatKind(err) != errors.InvalidInput {
		t.Fatalf("GetInvocation() invalid record error = %v, want invalid input", err)
	}
}

type unsupportedContent struct{}

func (unsupportedContent) Kind() alfred.ContentKind {
	return "unsupported"
}

func createTestSession(
	t *testing.T,
	store *Store,
	projectID alfred.ProjectID,
	id alfred.SessionID,
) alfred.Session {
	t.Helper()
	session := alfred.Session{ID: id, ProjectID: projectID}
	if err := store.CreateSession(t.Context(), session); err != nil {
		t.Fatalf("CreateSession(%q) error = %v", id, err)
	}
	return session
}
