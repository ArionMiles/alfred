package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestProviderOnboardingValidatesSavesAndPrefillsThroughRPC(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{"slash", "palette"} {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			model, _ := newProviderTestModel(t)
			model.composer.SetValue("conversation draft")
			if entry == "slash" {
				model.composer.SetValue("/providers")
			} else {
				opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				model = opened.(Model)
				model.palette.Select(5)
			}
			opened, load := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = opened.(Model)
			if !strings.Contains(model.View().Content, "Loading providers") {
				t.Fatal("missing provider loading state")
			}
			loaded, _ := model.Update(load())
			model = selectProviderForm(t, loaded.(Model), "openai")
			for range 2 {
				moved, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
				model = moved.(Model)
			}
			started, save := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			validated, _ := started.(Model).Update(save())
			model = validated.(Model)
			if model.providers.form.focused != 0 || !strings.Contains(model.View().Content, "This field is required.") {
				t.Fatal("required API-key error did not focus the field")
			}
			typed, _ := model.Update(tea.KeyPressMsg{Code: 'd', Text: "dummy-local-secret"})
			model = typed.(Model)
			form := model.providers.form
			if form.fields[0].input.Value() != "dummy-local-secret" || form.fields[0].input.EchoMode != textinput.EchoPassword ||
				strings.Contains(model.View().Content, "dummy-local-secret") {
				t.Fatal("API-key input was not accepted and masked")
			}
			form.fields[1].input.SetValue("https://example.test/v1")
			form.focus(len(form.fields))
			started, save = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = started.(Model)
			closed, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if closed.(Model).providers == nil || !model.providers.saving {
				t.Fatal("in-flight save was dismissed")
			}
			completed, _ := model.Update(save())
			model = completed.(Model)
			if !model.providers.saved || model.providers.form != nil || !strings.Contains(model.View().Content, "Settings saved.") {
				t.Fatal("save did not confirm success and release secret inputs")
			}
			dismissed, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = dismissed.(Model)
			if model.palette != nil || !model.composer.Focused() || (entry == "palette" && model.composer.Value() != "conversation draft") {
				t.Fatal("provider onboarding did not restore the conversation draft and focus")
			}
			opened, load = model.openProviders()
			loaded, _ = opened.(Model).Update(load())
			model = selectProviderForm(t, loaded.(Model), "openai")
			if model.providers.form.fields[0].input.Value() != "" || model.providers.form.fields[1].input.Value() != "https://example.test/v1" {
				t.Fatal("reopened form did not prefill only non-secret settings")
			}
		})
	}
}

func TestBedrockOnboardingCancelAndSave(t *testing.T) {
	t.Parallel()
	model, store := newProviderTestModel(t)
	model.gates.apply(inputRequestedEvent(1, "run-1", "input-1", "Question"))
	model.gateInput.SetValue("response draft")
	opened, load := model.openProviders()
	loaded, _ := opened.(Model).Update(load())
	model = selectProviderForm(t, loaded.(Model), "aws-bedrock")
	form := model.providers.form
	form.fields[0].input.SetValue("bedrock-profile")
	form.fields[1].input.SetValue("us-east-1")
	form.focus(len(form.fields) + 1)
	canceled, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = canceled.(Model)
	if model.providers != nil || model.gateInput.Value() != "response draft" || !model.gateInput.Focused() {
		t.Fatal("Cancel failed to return to the requested-input draft")
	}
	if _, err := store.GetProviderInstance(t.Context(), "aws-bedrock"); errors.WhatKind(err) != errors.NotFound {
		t.Fatal("Cancel persisted provider settings")
	}
	opened, load = model.openProviders()
	loaded, _ = opened.(Model).Update(load())
	model = selectProviderForm(t, loaded.(Model), "aws-bedrock")
	form = model.providers.form
	form.fields[0].input.SetValue("bedrock-profile")
	form.fields[1].input.SetValue("us-east-1")
	form.focus(len(form.fields))
	started, save := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	saved, _ := started.(Model).Update(save())
	instance, err := store.GetProviderInstance(t.Context(), "aws-bedrock")
	if err != nil || !saved.(Model).providers.saved || instance.Settings["profile"] != "bedrock-profile" ||
		instance.Settings["region"] != "us-east-1" || instance.CredentialRef != "" {
		t.Fatal("Bedrock settings did not save without a secret credential")
	}
}

func TestProviderSetupIgnoresDismissedRequestsAndRetainsFailedForm(t *testing.T) {
	const op = "TestProviderSetupIgnoresDismissedRequestsAndRetainsFailedForm"

	t.Parallel()
	model, _ := newProviderTestModel(t)
	opened, firstLoad := model.openProviders()
	model = opened.(Model)
	dismissed, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	opened, load := dismissed.(Model).openProviders()
	model = opened.(Model)
	ignored, _ := model.Update(firstLoad())
	model = ignored.(Model)
	if model.providers.loading == "" {
		t.Fatal("dismissed request replaced a newer picker")
	}
	loaded, _ := model.Update(load())
	model = selectProviderForm(t, loaded.(Model), "openai")
	model.providers.form.fields[0].input.SetValue("retained-secret")
	model.providers.saving = true
	failed, _ := model.Update(providerSavedMsg{
		setup: model.providers, err: errors.B.Op(op).KindUnavailable().UserMsg("Saving is unavailable.").Build(),
	})
	model = failed.(Model)
	if model.providers.saving || model.providers.form.fields[0].input.Value() != "retained-secret" ||
		!strings.Contains(model.View().Content, "Saving is unavailable.") || strings.Contains(model.View().Content, "retained-secret") {
		t.Fatalf("save failure lost the masked form or failed to display feedback: %q", model.View().Content)
	}
}

func TestProviderFormSelectUsesDeclaredValuesAndNavigates(t *testing.T) {
	t.Parallel()
	form, err := newSetupForm([]*alfredv1.SetupField{{
		Id: "region", Label: "Region", Kind: alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SELECT,
		Options: []*alfredv1.SetupFieldOption{{Value: "west", Label: "West"}, {Value: "east", Label: "East"}},
	}}, map[string]string{"region": "east"})
	if err != nil {
		t.Fatal(err)
	}
	model := readyModel(t)
	model.providers = &providerSetup{form: form}
	model.resizeProviderForm()
	form.focus(0)
	updated, _ := model.updateProviderFormKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if updated.(Model).providers.form.values()["region"] != "west" {
		t.Fatal("select input did not use the declared option value")
	}
	model.providers.form.focus(1)
	updated, _ = model.updateProviderFormKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if updated.(Model).providers.form.focused != 0 {
		t.Fatal("Shift+Tab did not return to the field")
	}
}

func TestProviderAuthorizationOpensBrowserAndCompletes(t *testing.T) {
	t.Parallel()
	model, _ := newProviderTestModel(t)
	var openedURL string
	model.openURL = func(_ context.Context, address string) error {
		openedURL = address
		return nil
	}
	opened, load := model.openProviders()
	loaded, _ := opened.(Model).Update(load())
	model = loaded.(Model)
	for index, item := range model.palette.Items() {
		if item.(providerChoice).definition.GetType() == "openai" {
			model.palette.Select(index)
		}
	}
	selected, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = selected.(Model)
	model.palette.Select(1)
	starting, start := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	started, _ := starting.(Model).Update(start())
	model = started.(Model)
	if openedURL != "https://auth.example.test/authorize" || model.providers.authorization.GetId() != "authorization-1" ||
		!strings.Contains(model.View().Content, openedURL) {
		t.Fatalf("authorization was not opened and displayed: %q", model.View().Content)
	}
	completed, _ := model.Update(providerAuthorizationPollMsg{
		setup: model.providers,
		authorization: &alfredv1.ProviderAuthorization{
			Id: "authorization-1", State: alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_COMPLETED,
			Message: "OpenAI account connected.", AccountLabel: "person@example.test",
		},
	})
	model = completed.(Model)
	if !model.providers.saved || !strings.Contains(model.View().Content, "person@example.test") {
		t.Fatalf("completed authorization was not shown: %q", model.View().Content)
	}
	closed, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if closed.(Model).providers != nil {
		t.Fatal("completed authorization did not close")
	}
}

type providerAuthorizerStub struct{}

func TestProviderAuthorizationPollRecoversAfterDaemonRestart(t *testing.T) {
	t.Parallel()
	model, _ := newProviderTestModel(t)
	palette := newPickerList("Providers", nil, 80, 24)
	model.palette = &palette
	model.providers = &providerSetup{
		definition: &alfredv1.ProviderDefinition{Name: "Example"},
		method:     &alfredv1.ProviderSetupMethod{Name: "Sign in"},
		authorization: &alfredv1.ProviderAuthorization{
			Id: "old-attempt", State: alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_PENDING,
		},
	}
	updated, retry := model.updateProviderAuthorizationPoll(providerAuthorizationPollMsg{
		setup: model.providers, err: connect.NewError(connect.CodeUnavailable, "daemon is restarting"),
	})
	model = updated.(Model)
	if retry == nil || model.providers.authorization.GetId() != "old-attempt" {
		t.Fatal("temporary polling failure lost the attempt")
	}
	if !strings.Contains(model.View().Content, "reopen Alfred to reconnect") {
		t.Fatal("polling failure did not explain reconnecting to a restarted daemon")
	}
	dismissed, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if dismissed.(Model).providers != nil {
		t.Fatal("unreachable daemon prevented dismissing authorization")
	}
	updated, retry = model.updateProviderAuthorizationPoll(providerAuthorizationPollMsg{
		setup: model.providers, err: connect.NewError(connect.CodeNotFound, "Authorization is no longer available. Reopen Providers and start again."),
	})
	model = updated.(Model)
	if retry != nil || !strings.Contains(model.View().Content, "Reopen Providers") ||
		model.providers.authorization.GetState() != alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_FAILED {
		t.Fatal("lost attempt was not shown as a recoverable, dismissible failure")
	}
	closed, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if closed.(Model).providers != nil {
		t.Fatal("lost authorization could not be dismissed")
	}
}

func (*providerAuthorizerStub) Start(
	context.Context,
	alfred.ProviderAuthorizationRequest,
) (alfred.ProviderAuthorization, error) {
	return alfred.ProviderAuthorization{
		ID: "authorization-1", State: alfred.ProviderAuthorizationPending,
		AuthorizationURL: "https://auth.example.test/authorize",
	}, nil
}

func (*providerAuthorizerStub) Get(
	context.Context,
	alfred.ProviderAuthorizationID,
) (alfred.ProviderAuthorization, error) {
	return alfred.ProviderAuthorization{
		ID: "authorization-1", State: alfred.ProviderAuthorizationCompleted,
		AccountLabel: "person@example.test", Message: "OpenAI account connected.",
	}, nil
}

func (*providerAuthorizerStub) Cancel(
	context.Context,
	alfred.ProviderAuthorizationID,
) (alfred.ProviderAuthorization, error) {
	return alfred.ProviderAuthorization{ID: "authorization-1", State: alfred.ProviderAuthorizationCanceled}, nil
}

func newProviderTestModel(t *testing.T) (Model, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(t.Context(), sqlite.Config{Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	setup, err := providers.NewSetupService(providers.SetupDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	submitter := providers.NewSubmitter(setup, store)
	server := connect.NewServer()
	alfredv1connect.RegisterProviderSetupServiceHandler(server, rpc.NewProviderSetupHandler(setup, submitter, store, &providerAuthorizerStub{}))
	model := readyModel(t)
	model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
	resized, _ := model.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	model = resized.(Model)
	return model, store
}

func selectProviderForm(t *testing.T, model Model, providerType string) Model {
	t.Helper()
	for index, item := range model.palette.Items() {
		if item.(providerChoice).definition.GetType() == providerType {
			model.palette.Select(index)
		}
	}
	selected, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	loading, prepare := selected.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	loaded, _ := loading.(Model).Update(prepare())
	model = loaded.(Model)
	if model.providers.form == nil {
		t.Fatalf("provider form was not prepared: %v", model.providers.err)
	}
	return model
}
