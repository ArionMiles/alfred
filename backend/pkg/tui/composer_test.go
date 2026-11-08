package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestComposerShowsMatchingSlashCommands(t *testing.T) {
	t.Parallel()

	composer := newComposer([]command{
		{name: "/new", description: "Start a new session"},
		{name: "/resume", description: "Resume a run"},
		{name: "/trust", description: "Toggle project skill trust"},
	})
	composer.SetValue("/r")

	view := composer.suggestionView()
	if !strings.Contains(view, "/resume") {
		t.Fatalf("View() = %q, want matching command", view)
	}
	if strings.Contains(view, "/new") || strings.Contains(view, "/trust") {
		t.Fatalf("View() = %q, contains commands that do not match", view)
	}
}

func TestComposerHidesCommandsForMessageInput(t *testing.T) {
	t.Parallel()

	composer := newComposer(commands)
	composer.SetValue("please /trust this project")

	if view := composer.suggestionView(); view != "" {
		t.Fatalf("suggestionView() = %q, want no suggestions", view)
	}
}

func TestComposerNavigatesAndAcceptsSuggestions(t *testing.T) {
	t.Parallel()

	composer := newComposer([]command{
		{name: "/resume", description: "Resume a run"},
		{name: "/restart", description: "Restart a run"},
	})
	composer.Focus()
	composer.SetValue("/r")
	composer, _ = composer.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if selected := composer.matches()[composer.selected].name; selected != "/restart" {
		t.Fatalf("selected command = %q, want /restart", selected)
	}
	composer, _ = composer.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if value := composer.Value(); value != "/restart" {
		t.Fatalf("Value() = %q, want /restart", value)
	}
}

func TestComposerShowsCanonicalCommandForAlias(t *testing.T) {
	t.Parallel()

	composer := newComposer(commands)
	composer.SetValue("/ex")

	view := composer.suggestionView()
	if !strings.Contains(view, "/quit") || strings.Contains(view, "/exit") {
		t.Fatalf("suggestionView() = %q, want canonical /quit only", view)
	}
}

func TestComposerCapsAndScrollsCommandMenu(t *testing.T) {
	t.Parallel()

	available := []command{
		{name: "/a"},
		{name: "/b"},
		{name: "/c"},
		{name: "/d"},
		{name: "/e"},
		{name: "/f"},
		{name: "/g"},
	}
	composer := newComposer(available)
	composer.Focus()
	composer.SetValue("/")
	if lines := strings.Count(composer.suggestionView(), "\n") + 1; lines != commandMenuHeight {
		t.Fatalf("menu height = %d, want %d", lines, commandMenuHeight)
	}
	for range commandMenuHeight {
		composer, _ = composer.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	view := composer.suggestionView()
	if !strings.Contains(view, "/g") || strings.Contains(view, "/a") {
		t.Fatalf("scrolled suggestionView() = %q", view)
	}
}
