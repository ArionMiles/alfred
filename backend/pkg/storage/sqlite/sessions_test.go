package sqlite

import (
	"testing"
	"time"

	modernsqlite "modernc.org/sqlite"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestSessionStoreLifecycle(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	project := createTestProject(t, store, "project-1")
	otherProject := createTestProject(t, store, "project-2")
	first := alfred.Session{
		ID: "session-1", ProjectID: project.ID, Title: "First",
		CreatedAt: time.Date(2026, time.October, 25, 9, 0, 0, 123, time.UTC),
	}
	second := alfred.Session{
		ID: "session-2", ProjectID: project.ID, Title: "Second",
		CreatedAt: first.CreatedAt,
	}
	other := alfred.Session{
		ID: "session-3", ProjectID: otherProject.ID, Title: "Other",
		CreatedAt: first.CreatedAt,
	}

	for _, session := range []alfred.Session{second, first, other} {
		if err := store.CreateSession(t.Context(), session); err != nil {
			t.Fatalf("CreateSession(%q) error = %v", session.ID, err)
		}
	}
	if err := store.SetSessionTitle(t.Context(), first.ID, "Renamed"); err != nil {
		t.Fatalf("SetSessionTitle() error = %v", err)
	}

	got, err := store.GetSession(t.Context(), first.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	want := first
	want.Title = "Renamed"
	if got != want {
		t.Fatalf("GetSession() = %#v, want %#v", got, want)
	}

	firstPage, err := store.ListSessions(
		t.Context(),
		project.ID,
		alfred.SessionListOptions{Limit: 1},
	)
	if err != nil {
		t.Fatalf("ListSessions() first page error = %v", err)
	}
	if len(firstPage.Sessions) != 1 || firstPage.Sessions[0].ID != first.ID || firstPage.Next == nil {
		t.Fatalf("ListSessions() first page = %#v, want first session and next cursor", firstPage)
	}
	secondPage, err := store.ListSessions(t.Context(), project.ID, alfred.SessionListOptions{
		Limit: 1,
		After: firstPage.Next,
	})
	if err != nil {
		t.Fatalf("ListSessions() second page error = %v", err)
	}
	if len(secondPage.Sessions) != 1 || secondPage.Sessions[0].ID != second.ID || secondPage.Next != nil {
		t.Fatalf("ListSessions() second page = %#v, want final second session", secondPage)
	}
}

func TestSessionStoreMapsMissingSession(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)

	if _, err := store.GetSession(t.Context(), "missing"); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("GetSession() error = %v, want not found", err)
	}
	if err := store.SetSessionTitle(t.Context(), "missing", "Title"); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("SetSessionTitle() error = %v, want not found", err)
	}
}

func TestCreateSessionMapsConstraintErrors(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	project := createTestProject(t, store, "project-1")
	session := alfred.Session{ID: "session-1", ProjectID: project.ID}
	if err := store.CreateSession(t.Context(), session); err != nil {
		t.Fatalf("CreateSession() first error = %v", err)
	}

	err := store.CreateSession(t.Context(), session)
	if errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("CreateSession() duplicate error = %v, want conflict", err)
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("CreateSession() duplicate error = %v, want wrapped *sqlite.Error", err)
	}

	err = store.CreateSession(t.Context(), alfred.Session{ID: "orphan", ProjectID: "missing"})
	if errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("CreateSession() orphan error = %v, want not found", err)
	}
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("CreateSession() orphan error = %v, want wrapped *sqlite.Error", err)
	}
}

func createTestProject(t *testing.T, store *Store, id alfred.ProjectID) alfred.Project {
	t.Helper()
	project := alfred.Project{
		ID:         id,
		Root:       "/projects/" + string(id),
		SkillTrust: alfred.ProjectSkillTrustUnknown,
		CreatedAt:  time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC),
	}
	if err := store.CreateProject(t.Context(), project); err != nil {
		t.Fatalf("CreateProject(%q) error = %v", id, err)
	}
	return project
}
