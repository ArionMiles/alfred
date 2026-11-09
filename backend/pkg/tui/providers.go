package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/pkg/x/errors"
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
	definition    *alfredv1.ProviderDefinition
	form          *setupForm
	method        *alfredv1.ProviderSetupMethod
	loading       string
	saving        bool
	canceling     bool
	saved         bool
	authorization *alfredv1.ProviderAuthorization
	browserErr    error
	err           error
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

type providerAuthorizationStartedMsg struct {
	setup         *providerSetup
	authorization *alfredv1.ProviderAuthorization
	browserErr    error
	err           error
}

type providerAuthorizationPollMsg struct {
	setup         *providerSetup
	authorization *alfredv1.ProviderAuthorization
	err           error
}

type providerAuthorizationCanceledMsg struct {
	setup         *providerSetup
	authorization *alfredv1.ProviderAuthorization
	err           error
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
	if setup.saving || setup.canceling {
		return model, nil
	}
	if setup.authorization != nil {
		if setup.authorization.GetState() == alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_PENDING {
			if message.String() == escapeKey {
				if setup.err != nil {
					model.providers = nil
					model.palette = nil
					return model, model.focusInput()
				}
				setup.canceling = true
				return model, model.cancelProviderAuthorization()
			}
			return model, nil
		}
		if message.String() == escapeKey || enter {
			model.providers = nil
			model.palette = nil
			return model, model.focusInput()
		}
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
			if selected.method.GetKind() == alfredv1.ProviderSetupMethodKind_PROVIDER_SETUP_METHOD_KIND_AUTHORIZATION {
				setup.loading = "Starting browser authorization…"
				setup.method = selected.method
				setup.err = nil
				return model, model.startProviderAuthorization(selected.method.GetId())
			}
			setup.loading = "Loading setup form…"
			setup.err = nil
			return model, model.prepareProviderForm(selected.method.GetId())
		}
	}
	return model.updatePalette(message)
}

func (model Model) startProviderAuthorization(methodID string) tea.Cmd {
	setup := model.providers
	providerType := setup.definition.GetType()
	return func() tea.Msg {
		response, err := model.clients.ProviderSetup.StartProviderAuthorization(model.ctx, &alfredv1.StartProviderAuthorizationRequest{
			Type: providerType, MethodId: methodID,
		})
		if err != nil {
			return providerAuthorizationStartedMsg{setup: setup, err: err}
		}
		authorization := response.GetAuthorization()
		var browserErr error
		if authorization.GetAuthorizationUrl() != "" {
			browserErr = model.openURL(model.ctx, authorization.GetAuthorizationUrl())
		}
		return providerAuthorizationStartedMsg{setup: setup, authorization: authorization, browserErr: browserErr}
	}
}

func (model Model) updateProviderAuthorizationStarted(message providerAuthorizationStartedMsg) (tea.Model, tea.Cmd) {
	if model.providers != message.setup {
		return model, nil
	}
	setup := model.providers
	setup.loading = ""
	setup.err = message.err
	setup.browserErr = message.browserErr
	setup.authorization = message.authorization
	if message.err != nil || message.authorization == nil {
		return model, nil
	}
	return model, model.pollProviderAuthorization(message.authorization.GetId())
}

func (model Model) pollProviderAuthorization(id string) tea.Cmd {
	setup := model.providers
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		response, err := model.clients.ProviderSetup.GetProviderAuthorization(model.ctx, &alfredv1.GetProviderAuthorizationRequest{
			AuthorizationId: id,
		})
		return providerAuthorizationPollMsg{setup: setup, authorization: response.GetAuthorization(), err: err}
	})
}

func (model Model) updateProviderAuthorizationPoll(message providerAuthorizationPollMsg) (tea.Model, tea.Cmd) {
	if model.providers != message.setup || model.providers.authorization == nil {
		return model, nil
	}
	setup := model.providers
	setup.err = message.err
	if message.err != nil {
		if connect.CodeOf(message.err) == connect.CodeNotFound {
			setup.authorization = &alfredv1.ProviderAuthorization{
				State:   alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_FAILED,
				Message: userErrorMessage(message.err),
			}
			setup.err = nil
			return model, nil
		}
		return model, model.pollProviderAuthorization(setup.authorization.GetId())
	}
	setup.authorization = message.authorization
	if message.authorization.GetState() == alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_PENDING {
		return model, model.pollProviderAuthorization(message.authorization.GetId())
	}
	setup.saved = message.authorization.GetState() == alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_COMPLETED
	return model, nil
}

func (model Model) cancelProviderAuthorization() tea.Cmd {
	setup := model.providers
	id := setup.authorization.GetId()
	return func() tea.Msg {
		response, err := model.clients.ProviderSetup.CancelProviderAuthorization(model.ctx, &alfredv1.CancelProviderAuthorizationRequest{
			AuthorizationId: id,
		})
		return providerAuthorizationCanceledMsg{setup: setup, authorization: response.GetAuthorization(), err: err}
	}
}

func (model Model) updateProviderAuthorizationCanceled(message providerAuthorizationCanceledMsg) (tea.Model, tea.Cmd) {
	if model.providers != message.setup {
		return model, nil
	}
	model.providers.canceling = false
	model.providers.err = message.err
	if message.err == nil {
		model.providers.authorization = message.authorization
	}
	return model, nil
}

func (model Model) prepareProviderForm(methodID string) tea.Cmd {
	setup := model.providers
	providerType := setup.definition.GetType()
	return func() tea.Msg {
		request := &alfredv1.PrepareProviderSetupRequest{Type: providerType, MethodId: methodID}
		response, err := model.clients.ProviderSetup.PrepareProviderSetup(model.ctx, request)
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
		Type: setup.definition.GetType(), MethodId: setup.method.GetId(), Values: setup.form.values(),
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
	case setup.authorization != nil:
		authorization := setup.authorization
		body := setup.definition.GetName() + " · " + setup.method.GetName() + "\n\n"
		switch authorization.GetState() {
		case alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_PENDING:
			body += "Finish signing in through your browser.\n\n" + authorization.GetAuthorizationUrl()
			if setup.browserErr != nil {
				body += "\n\nThe browser could not be opened automatically; open the URL above."
			}
			if setup.canceling {
				body += "\n\nCanceling authorization…"
			}
		case alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_COMPLETED:
			body += authorization.GetMessage()
			if authorization.GetAccountLabel() != "" {
				body += "\n" + authorization.GetAccountLabel()
			}
		default:
			body += authorization.GetMessage()
		}
		if setup.err != nil {
			body += "\n\n" + userErrorMessage(setup.err)
			if authorization.GetState() == alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_PENDING {
				body += "\nClose and reopen Alfred to reconnect. The browser authorization may still finish."
			}
		}
		return body
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
	if model.providers.saving || model.providers.canceling {
		return "Waiting for save to finish…"
	}
	if authorization := model.providers.authorization; authorization != nil {
		if authorization.GetState() == alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_PENDING {
			if model.providers.err != nil {
				return "esc close · reopen Alfred to reconnect"
			}
			return "esc cancel · copy/open the displayed URL if needed"
		}
		return closePanelHelp
	}
	if model.providers.form != nil {
		return "tab/shift+tab · enter select/advance · ↑/↓ options · esc cancel"
	}
	if model.providers.saved {
		return closePanelHelp
	}
	return "↑/↓ or tab/shift+tab · enter select · esc cancel"
}

func openBrowserURL(ctx context.Context, address string) error {
	const op = "openBrowserURL"

	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", address)
	case "linux":
		command = exec.CommandContext(ctx, "xdg-open", address)
	default:
		return errors.B.Op(op).KindUnavailable().Text("automatic browser opening is not supported on this operating system").Build()
	}
	if err := command.Run(); err != nil {
		return errors.B.Op(op).Text("open authorization URL").Err(err).Build()
	}
	return nil
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
