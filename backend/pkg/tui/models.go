package tui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

type modelItem struct {
	info    *alfredv1.ModelInfo
	current bool
}

func (item modelItem) Title() string {
	name := item.info.GetName()
	if name == "" {
		name = item.info.GetRef().GetModelId()
	}
	if item.current {
		name = "✓ " + name
	}
	return name
}

func (item modelItem) FilterValue() string { return item.Title() + " " + item.info.GetProviderName() }

// modelDelegate keeps provider labels in a quieter, right-aligned column.
// Clipping uses Lip Gloss so wide Unicode characters stay within terminal cells.
type modelDelegate struct{}

func (modelDelegate) Height() int                         { return 1 }
func (modelDelegate) Spacing() int                        { return 0 }
func (modelDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (modelDelegate) Render(writer io.Writer, palette list.Model, index int, item list.Item) {
	choice, ok := item.(modelItem)
	if !ok {
		return
	}
	width := palette.Width()
	available := max(width-2, 0)
	provider := clipModelLabel(choice.info.GetProviderName(), available/2)
	name := clipModelLabel(choice.Title(), max(available-lipgloss.Width(provider)-1, 0))
	marker := "  "
	if index == palette.Index() {
		marker = "› "
		name = lipgloss.NewStyle().Bold(true).Render(name)
	}
	gap := strings.Repeat(" ", max(available-lipgloss.Width(name)-lipgloss.Width(provider), 0))
	row := marker + name + gap + lipgloss.NewStyle().Faint(true).Render(provider)
	_, _ = fmt.Fprint(writer, clipModelLabel(row, width))
}

func clipModelLabel(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Inline(true).MaxWidth(width).Render(value)
}

type modelPicker struct {
	loading     bool
	saving      bool
	unavailable bool
	err         error
}

type modelsListedMsg struct {
	picker   *modelPicker
	models   []*alfredv1.ModelInfo
	selected *alfredv1.ModelRef
	err      error
}

type modelSelectedMsg struct {
	picker *modelPicker
	err    error
}

func (model Model) openModels() (tea.Model, tea.Cmd) {
	width, height := model.paletteSize()
	palette := newPickerList("Models", nil, width, height)
	palette.SetDelegate(modelDelegate{})
	palette.SetFilteringEnabled(true)
	model.palette = &palette
	model.models = &modelPicker{loading: true}
	model.composer.Blur()
	model.gateInput.Blur()
	picker := model.models
	return model, func() tea.Msg {
		choices, err := model.clients.Models.ListModels(model.ctx, &alfredv1.ListModelsRequest{})
		if err != nil {
			return modelsListedMsg{picker: picker, err: err}
		}
		selected, err := model.clients.Models.GetSelectedModel(model.ctx, &alfredv1.GetSelectedModelRequest{})
		return modelsListedMsg{picker: picker, models: choices.GetModels(), selected: selected.GetModel(), err: err}
	}
}

func (model Model) updateModelsListed(message modelsListedMsg) (tea.Model, tea.Cmd) {
	if model.models != message.picker {
		return model, nil
	}
	picker := model.models
	picker.loading = false
	picker.err = message.err
	if message.err != nil {
		return model, nil
	}
	picker.unavailable = message.selected != nil
	items := make([]list.Item, len(message.models))
	selectedIndex := 0
	for index, info := range message.models {
		current := sameModelRef(info.GetRef(), message.selected)
		items[index] = modelItem{info: info, current: current}
		if current {
			selectedIndex = index
			picker.unavailable = false
		}
	}
	palette := *model.palette
	command := palette.SetItems(items)
	palette.Select(selectedIndex)
	model.palette = &palette
	return model, command
}

func sameModelRef(left, right *alfredv1.ModelRef) bool {
	return left != nil && right != nil && left.GetProviderId() == right.GetProviderId() && left.GetModelId() == right.GetModelId()
}

func (model Model) updateModelPickerKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if model.models.saving {
		return model, nil
	}
	if model.palette.FilterState() == list.Filtering || (message.String() == escapeKey && model.palette.FilterState() == list.FilterApplied) {
		return model.updatePalette(message)
	}
	switch message.String() {
	case escapeKey:
		model.models = nil
		model.palette = nil
		return model, model.focusInput()
	case enterKey:
		if model.models.loading {
			return model, nil
		}
		selected, ok := model.palette.SelectedItem().(modelItem)
		if !ok {
			return model, nil
		}
		model.models.saving = true
		model.models.err = nil
		picker := model.models
		ref := selected.info.GetRef()
		return model, func() tea.Msg {
			_, err := model.clients.Models.SelectModel(model.ctx, &alfredv1.SelectModelRequest{Model: ref})
			return modelSelectedMsg{picker: picker, err: err}
		}
	}
	if model.models.loading {
		return model, nil
	}
	return model.updatePalette(message)
}

func (model Model) updateModelSelected(message modelSelectedMsg) (tea.Model, tea.Cmd) {
	if model.models != message.picker {
		return model, nil
	}
	model.models.saving = false
	model.models.err = message.err
	if message.err != nil {
		return model, nil
	}
	model.models = nil
	model.palette = nil
	model.actionErr = nil
	return model, model.focusInput()
}

func (model Model) modelPickerView() string {
	picker := model.models
	switch {
	case picker.loading:
		return "Models\n\nLoading models…"
	case picker.saving:
		return model.palette.View() + "\nSaving model selection…"
	case len(model.palette.Items()) == 0 && picker.err != nil:
		return "Models\n\nCould not load models: " + userErrorMessage(picker.err) + "\nDismiss and reopen to retry."
	case len(model.palette.Items()) == 0:
		return "Models\n\nNo models available.\nConnect a model provider in Providers (/providers), then reopen Models."
	}
	body := model.palette.View()
	if picker.unavailable {
		body += "\nThe previously selected model is unavailable. Choose another."
	}
	if picker.err != nil {
		body += "\nCould not select model: " + userErrorMessage(picker.err)
	}
	return body
}
