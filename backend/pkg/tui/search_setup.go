package tui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

type searchBackendItem struct{ definition *alfredv1.SearchBackend }

func (item searchBackendItem) Title() string       { return item.definition.GetName() }
func (item searchBackendItem) Description() string { return "" }
func (item searchBackendItem) FilterValue() string { return item.Title() }

type searchSetup struct {
	backend *alfredv1.SearchBackend
	form    *setupForm
	loading bool
	saving  bool
	saved   bool
	err     error
}
type (
	searchBackendsMsg struct {
		owner    *searchSetup
		response *alfredv1.ListSearchBackendsResponse
		err      error
	}
	searchFormMsg struct {
		owner    *searchSetup
		response *alfredv1.PrepareSearchSetupResponse
		err      error
	}
	searchSavedMsg struct {
		owner    *searchSetup
		response *alfredv1.SaveSearchSetupResponse
		err      error
	}
)

func (model Model) openSearchSetup() (tea.Model, tea.Cmd) {
	width, height := model.paletteSize()
	palette := newPickerList("Web search", nil, width, height)
	model.palette = &palette
	model.searchSetup = &searchSetup{loading: true}
	model.composer.Blur()
	model.gateInput.Blur()
	owner := model.searchSetup
	return model, func() tea.Msg {
		response, err := model.clients.SearchSetup.ListSearchBackends(model.ctx, &alfredv1.ListSearchBackendsRequest{})
		return searchBackendsMsg{owner: owner, response: response, err: err}
	}
}

func (model Model) updateSearchBackends(message searchBackendsMsg) (tea.Model, tea.Cmd) {
	if model.searchSetup != message.owner {
		return model, nil
	}
	model.searchSetup.loading, model.searchSetup.err = false, message.err
	items := make([]list.Item, len(message.response.GetBackends()))
	for index, definition := range message.response.GetBackends() {
		items[index] = searchBackendItem{definition: definition}
	}
	palette := *model.palette
	command := palette.SetItems(items)
	model.palette = &palette
	return model, command
}

func (model Model) updateSearchSetupKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	setup := model.searchSetup
	if setup.saving {
		return model, nil
	}
	if message.String() == escapeKey || (setup.saved && message.String() == enterKey) {
		model.searchSetup = nil
		model.palette = nil
		return model, model.focusInput()
	}
	if setup.loading || setup.saved {
		return model, nil
	}
	if setup.form != nil {
		command, action := setup.form.updateKey(message)
		switch action {
		case formSave:
			return model.saveSearchSetup()
		case formCancel:
			return model.updateSearchSetupKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		}
		return model, command
	}
	if message.String() == enterKey {
		if selected, ok := model.palette.SelectedItem().(searchBackendItem); ok {
			setup.loading = true
			setup.err = nil
			return model, func() tea.Msg {
				response, err := model.clients.SearchSetup.PrepareSearchSetup(model.ctx, &alfredv1.PrepareSearchSetupRequest{Backend: selected.definition.GetId()})
				return searchFormMsg{owner: setup, response: response, err: err}
			}
		}
	}
	return model.updatePalette(message)
}

func (model Model) updateSearchForm(message searchFormMsg) (tea.Model, tea.Cmd) {
	if model.searchSetup != message.owner {
		return model, nil
	}
	setup := model.searchSetup
	setup.loading, setup.err = false, message.err
	if message.err != nil {
		return model, nil
	}
	setup.backend = message.response.GetBackend()
	form, err := newSetupForm(setup.backend.GetFields(), message.response.GetValues())
	setup.err = err
	if err != nil {
		return model, nil
	}
	setup.form = form
	width, _ := model.paletteSize()
	form.resize(width, model.height)
	return model, form.focus(0)
}

func (model Model) saveSearchSetup() (tea.Model, tea.Cmd) {
	setup := model.searchSetup
	setup.saving = true
	setup.err = nil
	setup.form.problems = nil
	request := &alfredv1.SaveSearchSetupRequest{Backend: setup.backend.GetId(), Values: setup.form.values()}
	return model, func() tea.Msg {
		response, err := model.clients.SearchSetup.SaveSearchSetup(model.ctx, request)
		return searchSavedMsg{owner: setup, response: response, err: err}
	}
}

func (model Model) updateSearchSaved(message searchSavedMsg) (tea.Model, tea.Cmd) {
	if model.searchSetup != message.owner {
		return model, nil
	}
	setup := model.searchSetup
	setup.saving, setup.err = false, message.err
	if message.err != nil {
		return model, nil
	}
	setup.form.problems = message.response.GetFieldErrors()
	setup.form.refresh(false)
	if len(setup.form.problems) != 0 {
		return model, setup.form.focus(0)
	}
	if message.response.GetSaved() {
		setup.form = nil
		setup.saved = true
	}
	return model, nil
}

func (model Model) searchSetupView() string {
	setup := model.searchSetup
	switch {
	case setup.loading:
		return "Web search\n\nLoading search setup…"
	case setup.saved:
		return "Web search\n\nSearch settings saved.\nAccess has not been verified."
	case setup.form != nil:
		body := setup.backend.GetName() + " · Web search\n\n" + setup.form.view()
		if setup.saving {
			return body + "\nSaving search settings…"
		}
		if setup.err != nil {
			return body + "\nCould not save search settings: " + userErrorMessage(setup.err)
		}
		return body
	case setup.err != nil:
		return "Web search\n\nCould not load search setup: " + userErrorMessage(setup.err) + "\nDismiss and reopen to retry."
	case len(model.palette.Items()) == 0:
		return "Web search\n\nNo search backends installed."
	default:
		return model.palette.View()
	}
}
