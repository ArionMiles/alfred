package sqlite

import (
	"testing"
	"time"

	modernsqlite "modernc.org/sqlite"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestProjectStoreLifecycle(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	first := alfred.Project{
		ID: "project-1", Name: "Alfred", Root: "/projects/alfred",
		SkillTrust: alfred.ProjectSkillTrustUnknown,
		CreatedAt:  time.Date(2026, time.October, 25, 8, 0, 0, 123, time.UTC),
	}
	second := alfred.Project{
		ID: "project-2", Name: "Notes", Root: "/projects/notes",
		SkillTrust: alfred.ProjectSkillTrustTrusted,
		CreatedAt:  first.CreatedAt,
	}

	if err := store.CreateProject(t.Context(), second); err != nil {
		t.Fatalf("CreateProject() second error = %v", err)
	}
	if err := store.CreateProject(t.Context(), first); err != nil {
		t.Fatalf("CreateProject() first error = %v", err)
	}
	if err := store.RenameProject(t.Context(), first.ID, "Alfred v2"); err != nil {
		t.Fatalf("RenameProject() error = %v", err)
	}
	if err := store.SetProjectSkillTrust(t.Context(), first.ID, alfred.ProjectSkillTrustDenied); err != nil {
		t.Fatalf("SetProjectSkillTrust() error = %v", err)
	}

	got, err := store.GetProject(t.Context(), first.ID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	want := first
	want.Name = "Alfred v2"
	want.SkillTrust = alfred.ProjectSkillTrustDenied
	if got != want {
		t.Fatalf("GetProject() = %#v, want %#v", got, want)
	}

	firstPage, err := store.ListProjects(t.Context(), alfred.ProjectListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("ListProjects() first page error = %v", err)
	}
	if len(firstPage.Projects) != 1 || firstPage.Projects[0].ID != first.ID || firstPage.Next == nil {
		t.Fatalf("ListProjects() first page = %#v, want first project and next cursor", firstPage)
	}
	secondPage, err := store.ListProjects(t.Context(), alfred.ProjectListOptions{
		Limit: 1,
		After: firstPage.Next,
	})
	if err != nil {
		t.Fatalf("ListProjects() second page error = %v", err)
	}
	if len(secondPage.Projects) != 1 || secondPage.Projects[0].ID != second.ID || secondPage.Next != nil {
		t.Fatalf("ListProjects() second page = %#v, want final second project", secondPage)
	}
}

func TestCreateProjectMapsConflictAndPreservesSQLiteError(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	project := alfred.Project{ID: "project-1", SkillTrust: alfred.ProjectSkillTrustUnknown}
	if err := store.CreateProject(t.Context(), project); err != nil {
		t.Fatalf("CreateProject() first error = %v", err)
	}

	err := store.CreateProject(t.Context(), project)
	if kind := errors.WhatKind(err); kind != errors.Conflict {
		t.Fatalf("errors.WhatKind() = %#v, want %#v", kind, errors.Conflict)
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("CreateProject() error = %v, want wrapped *sqlite.Error", err)
	}
}

func TestProjectStoreUsesUniqueRootLookup(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	first := alfred.Project{
		ID: "project-1", Root: "/projects/alfred", SkillTrust: alfred.ProjectSkillTrustUnknown,
		CreatedAt: time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC),
	}
	if err := store.CreateProject(t.Context(), first); err != nil {
		t.Fatalf("CreateProject() first error = %v", err)
	}

	got, err := store.GetProjectByRoot(t.Context(), first.Root)
	if err != nil {
		t.Fatalf("GetProjectByRoot() error = %v", err)
	}
	if got != first {
		t.Fatalf("GetProjectByRoot() = %#v, want %#v", got, first)
	}
	err = store.CreateProject(t.Context(), alfred.Project{
		ID: "project-2", Root: first.Root, SkillTrust: alfred.ProjectSkillTrustUnknown,
	})
	if errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("CreateProject() duplicate root error = %v, want conflict", err)
	}
}

func TestProjectStoreMapsMissingProject(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)

	if _, err := store.GetProject(t.Context(), "missing"); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("GetProject() error = %v, want not found", err)
	}
	if _, err := store.GetProjectByRoot(t.Context(), "/missing"); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("GetProjectByRoot() error = %v, want not found", err)
	}
	if err := store.RenameProject(t.Context(), "missing", "name"); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("RenameProject() error = %v, want not found", err)
	}
	if err := store.SetProjectSkillTrust(
		t.Context(),
		"missing",
		alfred.ProjectSkillTrustTrusted,
	); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("SetProjectSkillTrust() error = %v, want not found", err)
	}
}
