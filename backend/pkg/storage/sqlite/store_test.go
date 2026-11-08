package sqlite

import (
	"testing"

	"github.com/ArionMiles/alfred"
)

func TestOpenDoesNotReapplyMigrations(t *testing.T) {
	t.Parallel()
	config := newTestConfig(t)
	store, err := Open(t.Context(), config)
	if err != nil {
		t.Fatalf("Open() first error = %v", err)
	}
	project := alfred.Project{ID: "project-1", SkillTrust: alfred.ProjectSkillTrustUnknown}
	if err := store.CreateProject(t.Context(), project); err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() first error = %v", err)
	}

	reopened, err := Open(t.Context(), config)
	if err != nil {
		t.Fatalf("Open() second error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() second error = %v", err)
		}
	})
	if _, err := reopened.GetProject(t.Context(), project.ID); err != nil {
		t.Fatalf("GetProject() after reopen error = %v", err)
	}
}
