package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestResolverDiscoverSkillsUsesRootPrecedence(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	projectRoot := filepath.Join(base, "project")
	userHome := filepath.Join(base, "home")
	configHome := filepath.Join(base, "config")
	systemRoot := filepath.Join(base, "etc", "alfred", "skills")
	project := alfred.Project{
		ID:         "project-1",
		Root:       projectRoot,
		SkillTrust: alfred.ProjectSkillTrustTrusted,
	}

	roots := []string{
		filepath.Join(projectRoot, ".claude", "skills"),
		filepath.Join(userHome, ".claude", "skills"),
		filepath.Join(projectRoot, ".agents", "skills"),
		filepath.Join(userHome, ".agents", "skills"),
		filepath.Join(configHome, "alfred", "skills"),
		systemRoot,
	}
	for index, root := range roots {
		writeSkill(t, root, "shared", "source-"+string(rune('1'+index)))
	}

	resolver := newTestResolver(t, project, userHome, configHome, systemRoot)
	set, err := resolver.DiscoverSkills(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}
	catalog := set.Catalog()
	if len(catalog.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(catalog.Skills))
	}
	if catalog.Skills[0].Description != "source-1" {
		t.Fatalf("Description = %q, want %q", catalog.Skills[0].Description, "source-1")
	}
	if len(catalog.Diagnostics) != len(roots)-1 {
		t.Fatalf("len(Diagnostics) = %d, want %d", len(catalog.Diagnostics), len(roots)-1)
	}
	for _, diagnostic := range catalog.Diagnostics {
		if diagnostic.Kind != alfred.SkillDiagnosticShadowed {
			t.Fatalf("diagnostic Kind = %q, want %q", diagnostic.Kind, alfred.SkillDiagnosticShadowed)
		}
	}
}

func TestResolverDiscoverSkillsExcludesUntrustedProjectSkills(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	projectRoot := filepath.Join(base, "project")
	userHome := filepath.Join(base, "home")
	configHome := filepath.Join(base, "config")
	systemRoot := filepath.Join(base, "etc", "alfred", "skills")
	project := alfred.Project{
		ID:         "project-1",
		Root:       projectRoot,
		SkillTrust: alfred.ProjectSkillTrustDenied,
	}

	writeSkill(t, filepath.Join(projectRoot, ".claude", "skills"), "project-only", "project")
	writeSkill(t, filepath.Join(userHome, ".agents", "skills"), "user-skill", "user")

	resolver := newTestResolver(t, project, userHome, configHome, systemRoot)
	set, err := resolver.DiscoverSkills(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}
	catalog := set.Catalog()
	if len(catalog.Skills) != 1 || catalog.Skills[0].Name != "user-skill" {
		t.Fatalf("Skills = %#v, want only user-skill", catalog.Skills)
	}
	if len(catalog.Diagnostics) != 0 {
		t.Fatalf("Diagnostics = %#v, want none", catalog.Diagnostics)
	}
}

func TestResolverDiscoverSkillsReportsInvalidSkills(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	userHome := filepath.Join(base, "home")
	root := filepath.Join(userHome, ".claude", "skills", "broken")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("not frontmatter"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	project := alfred.Project{ID: "project-1", Root: filepath.Join(base, "project")}
	resolver := newTestResolver(
		t,
		project,
		userHome,
		filepath.Join(base, "config"),
		filepath.Join(base, "etc", "alfred", "skills"),
	)
	set, err := resolver.DiscoverSkills(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}
	catalog := set.Catalog()
	if len(catalog.Skills) != 0 {
		t.Fatalf("Skills = %#v, want none", catalog.Skills)
	}
	if len(catalog.Diagnostics) != 1 || catalog.Diagnostics[0].Kind != alfred.SkillDiagnosticInvalid {
		t.Fatalf("Diagnostics = %#v, want one invalid diagnostic", catalog.Diagnostics)
	}
}

func TestSkillSetResolveLoadsInstructionsAndResources(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	userHome := filepath.Join(base, "home")
	skillRoot := filepath.Join(userHome, ".agents", "skills")
	writeSkill(t, skillRoot, "code-review", "Reviews code")

	resourcePath := filepath.Join(skillRoot, "code-review", "references", "guide.md")
	if err := os.MkdirAll(filepath.Dir(resourcePath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(resourcePath, []byte("guide"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	project := alfred.Project{ID: "project-1", Root: filepath.Join(base, "project")}
	resolver := newTestResolver(
		t,
		project,
		userHome,
		filepath.Join(base, "config"),
		filepath.Join(base, "etc", "alfred", "skills"),
	)
	set, err := resolver.DiscoverSkills(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}
	skill, err := set.Resolve("code-review")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	content, err := skill.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if content.Instructions != "instructions" {
		t.Fatalf("Instructions = %q, want %q", content.Instructions, "instructions")
	}
	if len(content.Resources) != 1 {
		t.Fatalf("len(Resources) = %d, want 1", len(content.Resources))
	}
	if content.Resources[0].Name != "references/guide.md" {
		t.Fatalf("resource Name = %q, want %q", content.Resources[0].Name, "references/guide.md")
	}
	if content.Resources[0].URI != "file://"+resourcePath {
		t.Fatalf("resource URI = %q, want %q", content.Resources[0].URI, "file://"+resourcePath)
	}
}

func TestSkillSetResolveReturnsNotFound(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	project := alfred.Project{ID: "project-1", Root: filepath.Join(base, "project")}
	resolver := newTestResolver(
		t,
		project,
		filepath.Join(base, "home"),
		filepath.Join(base, "config"),
		filepath.Join(base, "etc", "alfred", "skills"),
	)

	set, err := resolver.DiscoverSkills(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}
	_, err = set.Resolve("missing")
	if kind := errors.WhatKind(err); kind != errors.NotFound {
		t.Fatalf("errors.WhatKind(error) = %#v, want %#v", kind, errors.NotFound)
	}
}

func newTestResolver(
	t *testing.T,
	project alfred.Project,
	userHome string,
	configHome string,
	systemRoot string,
) *Resolver {
	t.Helper()
	projects := mocks.NewMockProjectStore(gomock.NewController(t))
	projects.EXPECT().GetProject(gomock.Any(), project.ID).Return(project, nil)
	return NewResolver(Config{
		UserHome: userHome, ConfigHome: configHome, SystemRoot: systemRoot,
	}, projects)
}

func writeSkill(t *testing.T, root, name, description string) {
	t.Helper()

	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	data := []byte("---\nname: " + name + "\ndescription: " + description + "\n---\ninstructions\n")
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), data, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}
