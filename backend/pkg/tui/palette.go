package tui

import (
	"fmt"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	enterKey  = "enter"
	escapeKey = "esc"
)

func newPickerList(title string, items []list.Item, width, height int) list.Model {
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	palette := list.New(items, delegate, width, height)
	palette.Title = title
	palette.SetFilteringEnabled(false)
	palette.SetShowStatusBar(false)
	palette.SetShowHelp(false)
	palette.DisableQuitKeybindings()
	palette.KeyMap.CursorUp.SetKeys("up", "shift+tab")
	palette.KeyMap.CursorDown.SetKeys("down", "tab")
	return palette
}

func (model Model) togglePalette() (tea.Model, tea.Cmd) {
	if model.palette != nil {
		if model.searchSetup != nil && model.searchSetup.saving {
			return model, nil
		}
		if (model.providers != nil && model.providers.saving) || (model.models != nil && model.models.saving) {
			return model, nil
		}
		model.palette = nil
		model.sessions = nil
		model.providers = nil
		model.models = nil
		model.searchSetup = nil
		return model, model.focusInput()
	}
	if model.project == nil || !model.inspectedSkills || model.err != nil || model.settingTrust {
		return model, nil
	}
	width, height := model.paletteSize()
	items := make([]list.Item, len(commands))
	for index, entry := range commands {
		items[index] = entry
	}
	palette := newPickerList("Commands", items, width, height)
	model.palette = &palette
	model.composer.Blur()
	model.gateInput.Blur()
	return model, nil
}

func (model Model) updatePaletteKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if model.searchSetup != nil {
		return model.updateSearchSetupKey(message)
	}
	if model.models != nil {
		return model.updateModelPickerKey(message)
	}
	if model.providers != nil {
		return model.updateProviderKey(message)
	}
	switch message.String() {
	case escapeKey:
		model.palette = nil
		model.sessions = nil
		return model, model.focusInput()
	case enterKey:
		if model.sessions != nil {
			return model.selectSession()
		}
		if selected, ok := model.palette.SelectedItem().(command); ok {
			model.palette = nil
			return model.executeCommand(selected)
		}
	}
	return model.updatePalette(message)
}

func (model Model) updatePalette(message tea.Msg) (tea.Model, tea.Cmd) {
	if wheel, ok := message.(tea.MouseWheelMsg); ok && len(model.palette.VisibleItems()) > 0 {
		palette := *model.palette
		switch wheel.Button {
		case tea.MouseWheelUp:
			palette.CursorUp()
		case tea.MouseWheelDown:
			palette.CursorDown()
		}
		model.palette = &palette
		return model, nil
	}
	palette, command := model.palette.Update(message)
	model.palette = &palette
	return model, command
}

func (model Model) updatePaletteActivity(message tea.Msg) (tea.Model, tea.Cmd) {
	if model.searchSetup != nil && model.searchSetup.form != nil {
		if model.searchSetup.saving {
			return model, nil
		}
		return model, model.searchSetup.form.update(message)
	}
	if model.models != nil && (model.models.loading || model.models.saving) {
		return model, nil
	}
	if model.providers != nil && model.providers.form != nil {
		return model.updateProviderForm(message)
	}
	return model.updatePalette(message)
}

func (model *Model) focusInput() tea.Cmd {
	if model.palette != nil || model.replaying {
		return nil
	}
	if gate, ok := model.gates.current(); ok {
		if gate.kind == inputGate {
			return model.gateInput.Focus()
		}
		return nil
	}
	if model.project != nil && model.inspectedSkills && !model.requiresSkillTrust() {
		return model.composer.Focus()
	}
	return nil
}

func (model Model) paletteSize() (int, int) {
	return max(min(model.composer.input.Width(), 64), 1), max(min(model.height-3, 8), 1)
}

func (model Model) overlayPalette(content string) string {
	width := model.composer.input.Width() + 2
	height := model.height
	if height == 0 {
		height = lipgloss.Height(content)
	}
	controls := "↑/↓ or tab/shift+tab · enter run · esc close"
	body := model.palette.View()
	if model.sessions != nil {
		controls = "↑/↓ or tab/shift+tab · enter open · esc close"
		switch {
		case model.sessions.loading:
			body = "Sessions\n\nLoading sessions…"
		case model.sessions.err != nil:
			body = fmt.Sprintf("Sessions\n\nCould not load sessions: %v", model.sessions.err)
		case len(model.palette.Items()) == 0:
			body = "Sessions\n\nNo sessions yet."
		}
	}
	if model.models != nil {
		body = model.modelPickerView()
		controls = "↑/↓ · tab/shift+tab · / filter · enter select · esc clear/close"
		if model.models.saving {
			controls = "Waiting for save to finish…"
		}
	}
	if model.providers != nil {
		body = model.providerView()
		controls = model.providerHelp()
	}
	if model.searchSetup != nil {
		body = model.searchSetupView()
		controls = "↑/↓ or tab/shift+tab · enter select · esc close"
		if model.searchSetup.form != nil {
			controls = "tab/shift+tab · enter select/advance · ↑/↓ options · esc cancel"
		}
		if model.searchSetup.saving {
			controls = "Saving search settings…"
		}
		if model.searchSetup.saved {
			controls = "enter or esc close"
		}
	}
	help := lipgloss.NewStyle().Width(model.palette.Width()).Render(controls)
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		Width(model.palette.Width()).MaxWidth(width).MaxHeight(height).Render(body + "\n" + help)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(content),
		lipgloss.NewLayer(panel).
			X(max((width-lipgloss.Width(panel))/2, 0)).
			Y(max((height-lipgloss.Height(panel))/2, 0)).Z(1),
	).Render()
}

func (model Model) executeCommand(selected command) (tea.Model, tea.Cmd) {
	switch selected.id {
	case trustCommand:
		model.settingTrust = true
		return model, model.setProjectSkillTrust(model.toggledSkillTrust())
	case quitCommand:
		model.stopWatching()
		return model, tea.Quit
	case sessionsCommand:
		return model.openSessions()
	case newSessionCommand:
		return model.newSession()
	case resumeCommand:
		return model.resumeRun()
	case providersCommand:
		return model.openProviders()
	case modelsCommand:
		return model.openModels()
	case searchSetupCommand:
		return model.openSearchSetup()
	}
	return model, nil
}
