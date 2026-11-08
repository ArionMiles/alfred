package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

const commandMenuHeight = 6

// composer owns text entry and slash-command discovery.
type composer struct {
	input    textinput.Model
	commands []command
	selected int
	offset   int
}

func newComposer(available []command) composer {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "Ask Alfred or enter a command"
	input.SetWidth(72)

	return composer{input: input, commands: available}
}

func (composer composer) Update(message tea.Msg) (composer, tea.Cmd) {
	if keyPress, ok := message.(tea.KeyPressMsg); ok && composer.hasSuggestions() {
		switch keyPress.String() {
		case "up":
			composer.moveSelection(-1)
			return composer, nil
		case "down":
			composer.moveSelection(1)
			return composer, nil
		case "tab":
			composer.acceptSuggestion()
			return composer, nil
		}
	}

	previous := composer.input.Value()
	var update tea.Cmd
	composer.input, update = composer.input.Update(message)
	if composer.input.Value() != previous {
		composer.selected = 0
		composer.offset = 0
	}
	return composer, update
}

func (composer composer) View() string {
	return composer.input.View()
}

func (composer composer) suggestionView() string {
	matches := composer.matches()
	if len(matches) == 0 {
		return ""
	}

	end := min(composer.offset+commandMenuHeight, len(matches))
	lines := make([]string, 0, end-composer.offset)
	for index := composer.offset; index < end; index++ {
		marker := "  "
		if index == composer.selected {
			marker = "● "
		}
		lines = append(lines, fmt.Sprintf(
			"%s%-12s %s",
			marker,
			matches[index].name,
			matches[index].description,
		))
	}
	return strings.Join(lines, "\n")
}

func (composer composer) commandForEnter() (command, bool) {
	if exact, ok := findCommand(strings.TrimSpace(composer.input.Value())); ok {
		return exact, true
	}
	matches := composer.matches()
	if len(matches) == 1 {
		return matches[0], true
	}
	return command{}, false
}

func (composer composer) hasSuggestions() bool {
	return len(composer.matches()) > 0
}

func (composer *composer) acceptSuggestion() {
	matches := composer.matches()
	if len(matches) == 0 {
		return
	}
	composer.input.SetValue(matches[composer.selected].name)
	composer.selected = 0
	composer.offset = 0
}

func (composer *composer) moveSelection(change int) {
	count := len(composer.matches())
	if count == 0 {
		return
	}
	composer.selected = (composer.selected + change + count) % count
	if composer.selected < composer.offset {
		composer.offset = composer.selected
	}
	if composer.selected >= composer.offset+commandMenuHeight {
		composer.offset = composer.selected - commandMenuHeight + 1
	}
}

func (composer composer) matches() []command {
	value := composer.input.Value()
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t\n") {
		return nil
	}

	matches := make([]command, 0, len(composer.commands))
	for _, candidate := range composer.commands {
		if strings.HasPrefix(candidate.name, value) || matchesAlias(candidate.aliases, value) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func matchesAlias(aliases []string, value string) bool {
	for _, alias := range aliases {
		if strings.HasPrefix(alias, value) {
			return true
		}
	}
	return false
}

func (composer *composer) SetValue(value string) {
	composer.input.SetValue(value)
	composer.selected = 0
	composer.offset = 0
}

func (composer composer) Value() string {
	return composer.input.Value()
}

func (composer *composer) Reset() {
	composer.input.Reset()
	composer.selected = 0
	composer.offset = 0
}

func (composer *composer) Focus() tea.Cmd {
	return composer.input.Focus()
}

func (composer *composer) Blur() {
	composer.input.Blur()
}

func (composer composer) Focused() bool {
	return composer.input.Focused()
}

func (composer *composer) SetWidth(width int) {
	composer.input.SetWidth(width)
}
