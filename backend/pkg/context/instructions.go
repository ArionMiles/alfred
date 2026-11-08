package context

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

//go:embed templates/system.md
var systemInstructions string

func (builder *Builder) instructions(ctx context.Context, request alfred.ContextRequest) (string, error) {
	const op = "instructions"
	project, err := builder.projects.GetProject(ctx, request.ProjectID)
	if err != nil {
		return "", errors.B.Op(op).Err(err).Build()
	}
	sections := []string{strings.TrimSpace(systemInstructions), fmt.Sprintf("Project directory: %s", project.Root)}
	for _, directory := range []string{builder.config.ConfigDir, project.Root} {
		if directory == "" {
			continue
		}
		text, err := readInstructions(directory)
		if err != nil {
			return "", errors.B.Op(op).Err(err).Build()
		}
		if text != "" {
			sections = append(sections, text)
		}
	}
	skills, err := builder.skillCatalogInstructions(request.SkillCatalog)
	if err != nil {
		return "", errors.B.Op(op).Err(err).Build()
	}
	if skills != "" {
		sections = append(sections, skills)
	}
	return strings.Join(sections, "\n\n"), nil
}

func readInstructions(directory string) (string, error) {
	const op = "readInstructions"
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(directory, name)
		// #nosec G703 -- Instruction roots are supplied by the user and project resolver.
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", errors.B.Op(op).Textf("read instructions %q", path).UserMsg("Alfred could not read an instruction file.").Err(err).Build()
		}
		if strings.TrimSpace(string(data)) == "" {
			return "", nil
		}
		return fmt.Sprintf("Instructions from %s:\n\n%s", path, data), nil
	}
	return "", nil
}
