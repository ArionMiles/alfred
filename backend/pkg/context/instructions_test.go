package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
)

func TestInstructionsComposeSystemGlobalProjectAndSkills(t *testing.T) {
	t.Parallel()
	global, project := t.TempDir(), t.TempDir()
	writeInstruction := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeInstruction(filepath.Join(global, "CLAUDE.md"), "Global preferences")
	writeInstruction(filepath.Join(project, "AGENTS.md"), "Project guidance")
	writeInstruction(filepath.Join(project, "CLAUDE.md"), "Do not load this fallback")
	projects := mocks.NewMockProjectStore(gomock.NewController(t))
	projects.EXPECT().GetProject(gomock.Any(), alfred.ProjectID("project")).Return(alfred.Project{Root: project, SkillTrust: alfred.ProjectSkillTrustDenied}, nil).Times(2)
	builder := New(Config{ConfigDir: global}, nil, nil, projects)
	request := alfred.ContextRequest{ProjectID: "project", SkillCatalog: alfred.SkillCatalog{Skills: []alfred.SkillDefinition{{Name: "review", Description: "Review carefully"}}}}
	text, err := builder.instructions(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"You are Alfred", global, project, "Global preferences", "Project guidance", "<name>review</name>"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in %s", expected, text)
		}
	}
	if strings.Contains(text, "Do not load") || strings.Index(text, "Global preferences") > strings.Index(text, "Project guidance") || strings.Index(text, "Project guidance") > strings.Index(text, "<name>review") {
		t.Fatalf("incorrect precedence: %s", text)
	}
	writeInstruction(filepath.Join(global, "AGENTS.md"), "New global guidance")
	updated, err := builder.instructions(t.Context(), request)
	if err != nil || strings.Contains(updated, "Global preferences") || !strings.Contains(updated, "New global guidance") {
		t.Fatalf("updated=%s error=%v", updated, err)
	}
}

func TestInstructionFallbackDoesNotHideUnreadablePreferredFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("fallback"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readInstructions(root); err == nil {
		t.Fatal("invalid preferred file silently used fallback")
	}
	if text, err := readInstructions(t.TempDir()); err != nil || text != "" {
		t.Fatalf("missing=%s error=%v", text, err)
	}
}
