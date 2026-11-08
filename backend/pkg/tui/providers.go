package tui

import (
	"fmt"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

type providerChoice struct{ definition *alfredv1.ProviderDefinition }

func (item providerChoice) Title() string       { return item.definition.GetName() }
func (item providerChoice) Description() string { return item.definition.GetType() }
func (item providerChoice) FilterValue() string { return item.Title() }

type providerMethodChoice struct{ method *alfredv1.ProviderSetupMethod }

func (item providerMethodChoice) Title() string       { return item.method.GetName() }
func (item providerMethodChoice) Description() string { return "" }
func (item providerMethodChoice) FilterValue() string { return item.Title() }

type providerSetup struct {
	definition *alfredv1.ProviderDefinition
	form       *setupForm
	method     *alfredv1.ProviderSetupMethod
	loading    string
	saving     bool
	saved      bool
	err        error
}

type providerDefinitionsMsg struct {
	setup       *providerSetup
	definitions []*alfredv1.ProviderDefinition
	err         error
}

type providerFormMsg struct {
	setup *providerSetup
	form  *alfredv1.ProviderSetupForm
	err   error
}

type providerSavedMsg struct {
	setup  *providerSetup
	result *alfredv1.SubmitProviderSetupResponse
	err    error
}

func (model Model) openProviders() (tea.Model, tea.Cmd) {
	width, height := model.paletteSize()
	palette := newPickerList("Providers", nil, width, height)
	model.palette = &palette
	model.providers = &providerSetup{loading: "Loading providers…"}
	model.composer.Blur()
	model.gateInput.Blur()
	setup := model.providers
	return model, func() tea.Msg {
		response, err := model.clients.ProviderSetup.ListProviderDefinitions(model.ctx, &alfredv1.ListProviderDefinitionsRequest{})
		return providerDefinitionsMsg{setup: setup, definitions: response.GetDefinitions(), err: err}
	}
}

func (model Model) updateProviderDefinitions(message providerDefinitionsMsg) (tea.Model, tea.Cmd) {
	if model.providers != message.setup {
		return model, nil
	}
	model.providers.loading = ""
	model.providers.err = message.err
	items := make([]list.Item, len(message.definitions))
	for index, definition := range message.definitions {
		items[index] = providerChoice{definition: definition}
	}
	palette := *model.palette
	command := palette.SetItems(items)
	model.palette = &palette
	return model, command
}

func (model Model) updateProviderKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	setup := model.providers
	enter := message.Code == tea.KeyEnter && message.Mod == 0
	if setup.saving {
		return model, nil
	}
	if message.String() == escapeKey || (setup.saved && enter) {
		model.providers = nil
		model.palette = nil
		return model, model.focusInput()
	}
	if setup.loading != "" || setup.saved {
		return model, nil
	}
	if setup.form != nil {
		return model.updateProviderFormKey(message)
	}
	if enter {
		switch selected := model.palette.SelectedItem().(type) {
		case providerChoice:
			setup.definition = selected.definition
			setup.err = nil
			items := make([]list.Item, len(selected.definition.GetMethods()))
			for index, method := range selected.definition.GetMethods() {
				items[index] = providerMethodChoice{method: method}
			}
			width, height := model.paletteSize()
			palette := newPickerList(selected.Title(), items, width, height)
			model.palette = &palette
			return model, nil
		case providerMethodChoice:
			setup.loading = "Loading setup form…"
			setup.err = nil
			return model, model.prepareProviderForm(selected.method.GetId())
		}
	}
	return model.updatePalette(message)
}

func (model Model) prepareProviderForm(methodID string) tea.Cmd {
	setup := model.providers
	providerType := setup.definition.GetType()
	return func() tea.Msg {
		request := &alfredv1.PrepareProviderSetupRequest{Type: providerType, MethodId: methodID, InstanceId: providerType}
		response, err := model.clients.ProviderSetup.PrepareProviderSetup(model.ctx, request)
		if connect.CodeOf(err) == connect.CodeNotFound {
			request.InstanceId = ""
			response, err = model.clients.ProviderSetup.PrepareProviderSetup(model.ctx, request)
		}
		return providerFormMsg{setup: setup, form: response.GetForm(), err: err}
	}
}

func (model Model) updateProviderFormLoaded(message providerFormMsg) (tea.Model, tea.Cmd) {
	if model.providers != message.setup {
		return model, nil
	}
	setup := model.providers
	setup.loading = ""
	setup.err = message.err
	if message.err != nil {
		return model, nil
	}
	setup.method = message.form.GetMethod()
	form, err := newSetupForm(setup.method.GetFields(), message.form.GetValues())
	setup.err = err
	if err != nil {
		return model, nil
	}
	setup.form = form
	model.resizeProviderForm()
	return model, form.focus(0)
}

func (model Model) saveProviderForm() (tea.Model, tea.Cmd) {
	setup := model.providers
	setup.saving = true
	setup.err = nil
	setup.form.problems = nil
	setup.form.refresh(false)
	request := &alfredv1.SubmitProviderSetupRequest{
		InstanceId: setup.definition.GetType(), Type: setup.definition.GetType(), Name: setup.definition.GetName(),
		MethodId: setup.method.GetId(), Enabled: true, Values: setup.form.values(),
	}
	return model, func() tea.Msg {
		response, err := model.clients.ProviderSetup.SubmitProviderSetup(model.ctx, request)
		return providerSavedMsg{setup: setup, result: response, err: err}
	}
}

func (model Model) updateProviderSaved(message providerSavedMsg) (tea.Model, tea.Cmd) {
	if model.providers != message.setup {
		return model, nil
	}
	setup := model.providers
	setup.saving = false
	setup.err = message.err
	if message.err != nil {
		return model, nil
	}
	setup.form.problems = message.result.GetFieldErrors()
	if len(setup.form.problems) != 0 {
		for index := range setup.form.fields {
			field := &setup.form.fields[index]
			if field.definition.GetId() == setup.form.problems[0].GetFieldId() {
				return model, setup.form.focus(index)
			}
		}
		return model, nil
	}
	setup.form = nil // Drop secret inputs as soon as the submission succeeds.
	setup.saved = true
	return model, nil
}

func (model Model) providerView() string {
	setup := model.providers
	switch {
	case setup.loading != "":
		return "Providers\n\n" + setup.loading
	case setup.saved:
		return setup.definition.GetName() + "\n\nSettings saved.\nAccess has not been verified."
	case setup.form != nil:
		body := setup.definition.GetName() + " · " + setup.method.GetName() + "\n\n" + setup.form.view()
		if setup.saving {
			return body + "\nSaving settings…"
		}
		if setup.err != nil {
			return body + "\nCould not save settings: " + userErrorMessage(setup.err)
		}
		return body
	case setup.err != nil:
		return fmt.Sprintf("Providers\n\nCould not load setup: %s\nDismiss and reopen to retry.", userErrorMessage(setup.err))
	case len(model.palette.Items()) == 0:
		return "Providers\n\nNo setup methods available."
	default:
		return model.palette.View()
	}
}

func (model Model) providerHelp() string {
	if model.providers.saving {
		return "Waiting for save to finish…"
	}
	if model.providers.form != nil {
		return "tab/shift+tab · enter select/advance · ↑/↓ options · esc cancel"
	}
	if model.providers.saved {
		return "enter or esc close"
	}
	return "↑/↓ or tab/shift+tab · enter select · esc cancel"
}

func (model Model) updateProviderFormKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	command, action := model.providers.form.updateKey(message)
	switch action {
	case formSave:
		return model.saveProviderForm()
	case formCancel:
		return model.updateProviderKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	}
	return model, command
}

func (model Model) updateProviderForm(message tea.Msg) (tea.Model, tea.Cmd) {
	if model.providers.saving {
		return model, nil
	}
	return model, model.providers.form.update(message)
}

func (model *Model) resizeProviderForm() {
	width, _ := model.paletteSize()
	model.providers.form.resize(width, model.height)
}
