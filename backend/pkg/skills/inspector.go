package skills

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Inspector reports project skill roots without reading their contents.
type Inspector struct {
	projects alfred.ProjectStore
}

// NewInspector creates a project skill inspector.
func NewInspector(projects alfred.ProjectStore) *Inspector {
	return &Inspector{projects: projects}
}

// InspectProjectSkills reports existing project skill roots and their trust
// state without parsing or loading skills from those roots.
func (inspector *Inspector) InspectProjectSkills(
	ctx context.Context,
	projectID alfred.ProjectID,
) (alfred.ProjectSkillTrustState, error) {
	const op = "InspectProjectSkills"

	project, err := inspector.projects.GetProject(ctx, projectID)
	if err != nil {
		return alfred.ProjectSkillTrustState{}, errors.B.
			Op(op).
			Textf("get project %q", projectID).
			Err(err).
			Build()
	}

	state := alfred.ProjectSkillTrustState{
		ProjectID: project.ID,
		Trust:     project.SkillTrust,
	}
	for _, root := range projectSkillRoots(project.Root) {
		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return alfred.ProjectSkillTrustState{}, errors.B.
				Op(op).
				Textf("inspect project skill root %q", root).
				UserMsg("Alfred could not inspect the project's skill directories").
				Err(err).
				Build()
		}
		if info.IsDir() {
			state.Roots = append(state.Roots, root)
		}
	}

	return state, nil
}

func projectSkillRoots(projectRoot string) []string {
	return []string{
		filepath.Join(projectRoot, ".claude", "skills"),
		filepath.Join(projectRoot, ".agents", "skills"),
	}
}

var _ alfred.ProjectSkillInspector = (*Inspector)(nil)
