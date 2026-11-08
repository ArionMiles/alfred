package skills

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
)

func TestInspectorReportsExistingProjectSkillRoots(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	claudeRoot := filepath.Join(projectRoot, ".claude", "skills")
	if err := os.MkdirAll(claudeRoot, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}

	project := alfred.Project{
		ID:         "project-1",
		Root:       projectRoot,
		SkillTrust: alfred.ProjectSkillTrustUnknown,
	}
	projects := mocks.NewMockProjectStore(gomock.NewController(t))
	projects.EXPECT().GetProject(gomock.Any(), project.ID).Return(project, nil)
	inspector := NewInspector(projects)

	state, err := inspector.InspectProjectSkills(t.Context(), project.ID)
	if err != nil {
		t.Fatalf("InspectProjectSkills() error = %v", err)
	}
	if state.ProjectID != project.ID {
		t.Fatalf("ProjectID = %q, want %q", state.ProjectID, project.ID)
	}
	if state.Trust != alfred.ProjectSkillTrustUnknown {
		t.Fatalf("Trust = %q, want %q", state.Trust, alfred.ProjectSkillTrustUnknown)
	}
	if len(state.Roots) != 1 || state.Roots[0] != claudeRoot {
		t.Fatalf("Roots = %q, want [%q]", state.Roots, claudeRoot)
	}
}
