package rpc

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
)

func TestProviderSetupRPCPersistsSettingsAndSeparatesCredentials(t *testing.T) {
	t.Parallel()
	client, store := newProviderSetupTestClient(t, providers.SetupDefinitions())
	definitions, err := client.ListProviderDefinitions(t.Context(), &alfredv1.ListProviderDefinitionsRequest{})
	if err != nil || len(definitions.GetDefinitions()) != 4 || definitions.GetDefinitions()[0].GetType() != "aws-bedrock" {
		t.Fatalf("definitions = %v, error = %v", definitions, err)
	}
	prepared, err := client.PrepareProviderSetup(t.Context(), &alfredv1.PrepareProviderSetupRequest{Type: "openai", MethodId: "api-key"})
	if err != nil || prepared.GetForm().GetInstanceId() != "openai" ||
		prepared.GetForm().GetMethod().GetFields()[0].GetKind() != alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SECRET {
		t.Fatalf("new form = %v, error = %v", prepared, err)
	}
	request := &alfredv1.SubmitProviderSetupRequest{
		Type: "openai", MethodId: "api-key",
		Values: map[string]string{"api_key": "test-secret-one", "base_url": "https://example.test/v1"},
	}
	saved, err := client.SubmitProviderSetup(t.Context(), request)
	if err != nil || saved.GetInstance().GetId() != "openai" || saved.GetInstance().GetName() != "OpenAI" ||
		!saved.GetInstance().GetEnabled() || len(saved.GetFieldErrors()) != 0 {
		t.Fatalf("saved = %v, error = %v", saved, err)
	}
	instance, err := store.GetProviderInstance(t.Context(), "openai")
	if err != nil || instance.CredentialRef == "" || instance.Settings["base_url"] != request.GetValues()["base_url"] {
		t.Fatalf("metadata was not persisted: %v", err)
	}
	if strings.Contains(saved.String(), "test-secret-one") || strings.Contains(saved.String(), string(instance.CredentialRef)) {
		t.Fatal("submission response exposed a credential")
	}
	credential, err := store.GetCredential(t.Context(), instance.CredentialRef)
	if err != nil {
		t.Fatalf("credential was not persisted: %v", err)
	}
	var record struct {
		Fields map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(credential, &record); err != nil || record.Fields["api_key"] != "test-secret-one" {
		t.Fatal("credential did not contain the submitted key")
	}
	prepared, err = client.PrepareProviderSetup(t.Context(), &alfredv1.PrepareProviderSetupRequest{Type: "openai", MethodId: "api-key"})
	if err != nil || len(prepared.GetForm().GetValues()) != 1 || prepared.GetForm().GetValues()["base_url"] != request.GetValues()["base_url"] ||
		strings.Contains(prepared.String(), "test-secret-one") {
		t.Fatalf("saved form did not expose only non-secret settings: %v", err)
	}
	request.Values["api_key"] = "test-secret-two"
	request.Values["base_url"] = "https://replacement.test/v1"
	if _, err := client.SubmitProviderSetup(t.Context(), request); err != nil {
		t.Fatalf("replace setup: %v", err)
	}
	replaced, err := store.GetProviderInstance(t.Context(), instance.ID)
	if err != nil || replaced.CredentialRef == instance.CredentialRef || replaced.Settings["base_url"] != request.GetValues()["base_url"] {
		t.Fatal("replacement did not save fresh settings and a fresh credential reference")
	}
}

func TestProviderSetupRPCValidationLeavesSavedSettingsUntouched(t *testing.T) {
	t.Parallel()
	client, store := newProviderSetupTestClient(t, providers.SetupDefinitions())
	before := alfred.ProviderInstance{ID: "openai", Type: "openai", Name: "Existing", Enabled: true, Settings: map[string]string{"base_url": "old"}}
	if err := store.PutProviderInstance(t.Context(), before); err != nil {
		t.Fatal(err)
	}
	result, err := client.SubmitProviderSetup(t.Context(), &alfredv1.SubmitProviderSetupRequest{
		Type: "openai", MethodId: "api-key", Values: map[string]string{"base_url": "new"},
	})
	if err != nil || result.GetInstance() != nil || len(result.GetFieldErrors()) != 1 || result.GetFieldErrors()[0].GetFieldId() != "api_key" {
		t.Fatalf("validation result = %v, error = %v", result, err)
	}
	after, err := store.GetProviderInstance(t.Context(), before.ID)
	if err != nil || after.Name != before.Name || after.Settings["base_url"] != "old" || after.CredentialRef != "" || !after.Enabled {
		t.Fatal("invalid submission changed saved settings")
	}
}

func TestProviderSetupRPCEncodesSelectFieldsAndRejectsInvalidChoice(t *testing.T) {
	t.Parallel()
	client, _ := newProviderSetupTestClient(t, []alfred.ProviderDefinition{{
		Type: "custom", Name: "Custom", Methods: []alfred.ProviderSetupMethod{{
			ID: "choose", Name: "Choose", Fields: []alfred.SetupField{{
				ID: "region", Label: "Region", Description: "Choose a region", Kind: alfred.SetupFieldSelect, Required: true,
				Options: []alfred.SetupFieldOption{{Value: "west", Label: "West"}},
			}},
		}},
	}})
	prepared, err := client.PrepareProviderSetup(t.Context(), &alfredv1.PrepareProviderSetupRequest{Type: "custom", MethodId: "choose"})
	if err != nil {
		t.Fatal(err)
	}
	field := prepared.GetForm().GetMethod().GetFields()[0]
	if field.GetKind() != alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SELECT || !field.GetRequired() ||
		field.GetOptions()[0].GetValue() != "west" || field.GetOptions()[0].GetLabel() != "West" || field.GetDescription() != "Choose a region" {
		t.Fatal("select field lost its declared rendering metadata")
	}
	result, err := client.SubmitProviderSetup(t.Context(), &alfredv1.SubmitProviderSetupRequest{
		Type: "custom", MethodId: "choose", Values: map[string]string{"region": "invalid-secret-value"},
	})
	if err != nil || len(result.GetFieldErrors()) != 1 || strings.Contains(result.String(), "invalid-secret-value") {
		t.Fatal("invalid select input did not return safe field feedback")
	}
}

func TestProviderSetupHandlerRejectsNilRequests(t *testing.T) {
	t.Parallel()
	handler := NewProviderSetupHandler(nil, nil, nil, nil)
	_, listErr := handler.ListProviderDefinitions(t.Context(), nil)
	_, prepareErr := handler.PrepareProviderSetup(t.Context(), nil)
	_, submitErr := handler.SubmitProviderSetup(t.Context(), nil)
	for _, err := range []error{listErr, prepareErr, submitErr} {
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("nil request code = %v", connect.CodeOf(err))
		}
	}
}

func newProviderSetupTestClient(t *testing.T, definitions []alfred.ProviderDefinition) (alfredv1connect.ProviderSetupServiceClient, *sqlite.Store) {
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
	setup, err := providers.NewSetupService(definitions)
	if err != nil {
		t.Fatal(err)
	}
	submitter := providers.NewSubmitter(setup, store)
	server := httptest.NewServer(NewServer(Handlers{
		Models: alfredv1connect.UnimplementedModelServiceHandler{},
		Daemon: alfredv1connect.UnimplementedDaemonServiceHandler{}, Project: alfredv1connect.UnimplementedProjectServiceHandler{},
		Invocation: alfredv1connect.UnimplementedInvocationServiceHandler{}, Session: alfredv1connect.UnimplementedSessionServiceHandler{},
		Run: alfredv1connect.UnimplementedRunServiceHandler{}, ProviderSetup: NewProviderSetupHandler(setup, submitter, store, nil),
	}))
	t.Cleanup(server.Close)
	return NewClients(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL))).ProviderSetup, store
}
