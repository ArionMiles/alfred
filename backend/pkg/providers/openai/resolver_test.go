package openai_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/identity"
	"github.com/ArionMiles/alfred/pkg/providers"
	provideropenai "github.com/ArionMiles/alfred/pkg/providers/openai"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestConfiguredProviderUsesSavedSetupAndPicksUpChanges(t *testing.T) {
	t.Parallel()
	store, submitter := newResolverSetup(t)
	type received struct {
		path          string
		authorization string
		model         string
	}
	requests := make(chan received, 2)
	var catalogRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/catalog" {
			catalogRequests.Add(1)
			_, _ = fmt.Fprint(writer, `{"openai":{"models":{"test-model":{"name":"Test model","tool_call":true,"reasoning":true,"modalities":{"input":["text"],"output":["text"]},"limit":{"context":128000,"output":16000}}}}}`)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- received{request.URL.Path, request.Header.Get("Authorization"), body.Model}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "data: "+`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Ready","annotations":[]}]}]}}`+"\n\n")
	}))
	t.Cleanup(server.Close)
	catalog := withDefaults[providers.CatalogConfig]()
	catalog.URL, catalog.CacheFile = server.URL+"/catalog", filepath.Join(t.TempDir(), "models.dev.json")
	var decorated []string
	layer := func(name string) providers.Middleware {
		return func(provider alfred.ModelProvider) alfred.ModelProvider {
			decorated = append(decorated, name)
			return provider
		}
	}
	registry, err := providers.NewRegistry(nil,
		providers.WithConfiguredProviders(provideropenai.NewResolver(
			store, store, nil, providers.NewCatalogSource(catalog, server.Client()), server.Client(),
		)),
		providers.WithMiddleware(layer("outer")), providers.WithMiddleware(layer("inner")),
	)
	if err != nil {
		t.Fatal(err)
	}
	submission := alfred.ProviderSetupSubmission{
		Type: "openai", MethodID: "api-key", Values: map[string]string{},
	}
	providerID := alfred.ProviderID(submission.Type)
	for index, version := range []string{"v1", "v2"} {
		key := "test-secret-" + version
		submission.Values["api_key"], submission.Values["base_url"] = key, server.URL+"/"+version
		if _, err := submitter.Submit(t.Context(), store, submission); err != nil {
			t.Fatal(err)
		}
		provider, err := registry.ResolveProvider(t.Context(), providerID)
		if err != nil {
			t.Fatal(err)
		}
		models, err := provider.Models(t.Context())
		if err != nil || len(models) != 1 || models[0].Ref.ProviderID != providerID || models[0].Name != "Test model" || !models[0].Capabilities.Reasoning {
			t.Fatalf("models=%#v error=%v", models, err)
		}
		model, err := provider.ResolveModel(t.Context(), "test-model")
		if err != nil {
			t.Fatal(err)
		}
		var kinds []alfred.ModelEventKind
		for event, err := range provider.Stream(t.Context(), alfred.ModelRequest{
			ID: "model-call", RunID: "run", Model: model.Ref,
			Input: []alfred.ModelInput{{Payload: alfred.Message{Role: alfred.MessageRoleUser, Content: []alfred.ContentPart{alfred.TextContent{Text: "Hello"}}}}},
		}) {
			if err != nil {
				t.Fatal(err)
			}
			kinds = append(kinds, event.Payload.ModelEventKind())
		}
		if !reflect.DeepEqual(kinds, []alfred.ModelEventKind{alfred.ModelEventKindMessage, alfred.ModelEventKindCompleted}) {
			t.Fatalf("event kinds=%v", kinds)
		}
		got := <-requests
		if got.path != "/"+version+"/responses" || got.authorization != "Bearer "+key || got.model != "test-model" {
			t.Fatal("runtime did not use the latest saved endpoint, key, and model")
		}
		if len(decorated) != (index+1)*2 {
			t.Fatalf("middleware applications=%v", decorated)
		}
	}
	if catalogRequests.Load() != 1 || !reflect.DeepEqual(decorated, []string{"inner", "outer", "inner", "outer"}) {
		t.Fatalf("catalog requests=%d middleware=%v", catalogRequests.Load(), decorated)
	}
}

func TestConfiguredProviderRejectsUnusableSetupBeforeNetworkAccess(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		instance   alfred.ProviderInstance
		credential string
		kind       errors.Kind
	}{
		{name: "missing", kind: errors.NotFound},
		{name: "disabled", instance: alfred.ProviderInstance{ID: "personal", Type: "openai"}, kind: errors.FailedPrecondition},
		{name: "unsupported", instance: alfred.ProviderInstance{ID: "personal", Type: "deepseek", Enabled: true}, kind: errors.Unimplemented},
		{name: "no_key", instance: alfred.ProviderInstance{ID: "personal", Type: "openai", Enabled: true}, kind: errors.FailedPrecondition},
		{name: "missing_credential", instance: alfred.ProviderInstance{ID: "personal", Type: "openai", Enabled: true, CredentialRef: "missing"}, kind: errors.NotFound},
		{name: "malformed", credential: `{"fields":{"api_key":"private-test-secret"`, kind: errors.FailedPrecondition},
		{name: "wrong_version", credential: `{"version":2,"provider":"openai","method":"api-key","fields":{"api_key":"private-test-secret"}}`, kind: errors.FailedPrecondition},
		{name: "wrong_provider", credential: `{"version":1,"provider":"deepseek","method":"api-key","fields":{"api_key":"private-test-secret"}}`, kind: errors.FailedPrecondition},
		{name: "wrong_method", credential: `{"version":1,"provider":"openai","method":"oauth","fields":{"api_key":"private-test-secret"}}`, kind: errors.FailedPrecondition},
		{name: "blank_key", credential: `{"version":1,"provider":"openai","method":"api-key","fields":{"api_key":" "}}`, kind: errors.FailedPrecondition},
		{name: "bad_url", instance: alfred.ProviderInstance{ID: "personal", Type: "openai", Enabled: true, Settings: map[string]string{"base_url": "file:///private-test-secret"}}, kind: errors.FailedPrecondition},
		{name: "unknown_setting", instance: alfred.ProviderInstance{ID: "personal", Type: "openai", Enabled: true, Settings: map[string]string{"api_key": "private-test-secret"}}, kind: errors.FailedPrecondition},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, _ := newResolverSetup(t)
			instance := test.instance
			if test.credential != "" {
				instance = alfred.ProviderInstance{ID: "personal", Type: "openai", Enabled: true, CredentialRef: "credential"}
				if err := store.PutCredential(t.Context(), instance.CredentialRef, []byte(test.credential)); err != nil {
					t.Fatal(err)
				}
			}
			if instance.ID != "" {
				if err := store.PutProviderInstance(t.Context(), instance); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid setup reached the network") }))
			t.Cleanup(server.Close)
			catalog := withDefaults[providers.CatalogConfig]()
			catalog.URL, catalog.CacheFile = server.URL, filepath.Join(t.TempDir(), "models.dev.json")
			registry, err := providers.NewRegistry(nil, providers.WithConfiguredProviders(provideropenai.NewResolver(
				store, store, nil, providers.NewCatalogSource(catalog, server.Client()), server.Client(),
			)))
			if err != nil {
				t.Fatal(err)
			}
			provider, err := registry.ResolveProvider(t.Context(), "personal")
			if provider != nil || errors.WhatKind(err) != test.kind {
				t.Fatalf("provider=%v error=%v", provider, err)
			}
			if strings.Contains(err.Error()+errors.UserMsg(err), "private-test-secret") {
				t.Fatal("setup resolution exposed secret content")
			}
			if test.kind != errors.Unimplemented && !errors.IsRetryable(err) {
				t.Fatalf("repairable setup error is not resumable: %v", err)
			}
		})
	}
}

func TestConfiguredProviderUsesSuppliedCredentialBackend(t *testing.T) {
	t.Parallel()
	store, _ := newResolverSetup(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/catalog" {
			_, _ = fmt.Fprint(writer, `{"openai":{"models":{"test-model":{"name":"Test model"}}}}`)
			return
		}
		if request.Header.Get("Authorization") != "Bearer external-test-key" {
			t.Error("provider did not use the supplied credential backend")
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "data: "+`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`+"\n\n")
	}))
	t.Cleanup(server.Close)
	instance := alfred.ProviderInstance{ID: "personal", Type: "openai", Enabled: true, CredentialRef: "external-ref", Settings: map[string]string{"base_url": server.URL + "/v1"}}
	if err := store.PutProviderInstance(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	credentials := mocks.NewMockCredentialReader(gomock.NewController(t))
	credentials.EXPECT().GetCredential(gomock.Any(), instance.CredentialRef).Return([]byte(`{"version":1,"provider":"openai","method":"api-key","fields":{"api_key":"external-test-key"}}`), nil)
	catalog := withDefaults[providers.CatalogConfig]()
	catalog.URL, catalog.CacheFile = server.URL+"/catalog", filepath.Join(t.TempDir(), "models.dev.json")
	registry, err := providers.NewRegistry(nil, providers.WithConfiguredProviders(provideropenai.NewResolver(
		store, credentials, nil, providers.NewCatalogSource(catalog, server.Client()), server.Client(),
	)))
	if err != nil {
		t.Fatal(err)
	}
	provider, err := registry.ResolveProvider(t.Context(), instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range provider.Stream(t.Context(), alfred.ModelRequest{ID: "c", Model: alfred.ModelRef{ProviderID: instance.ID, ModelID: "test-model"}}) {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfiguredProviderResolvesChatGPTSubscription(t *testing.T) {
	t.Parallel()
	store, _ := newResolverSetup(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/catalog" {
			t.Errorf("unexpected request path %q", request.URL.Path)
		}
		_, _ = fmt.Fprint(writer, `{"openai":{"models":{"test-model":{"name":"Test model"}}}}`)
	}))
	t.Cleanup(server.Close)
	instance := alfred.ProviderInstance{
		ID: "openai", Type: "openai", Name: "OpenAI (ChatGPT plan)", Enabled: true,
		CredentialRef: "subscription-ref", Settings: map[string]string{},
	}
	if err := store.PutProviderInstance(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	credential := map[string]any{
		"version": 1, "provider": "openai", "method": "chatgpt-subscription",
		"issuer": "https://auth.openai.com", "subject": "account-subject", "client_id": "issued-client",
		"access_token": "access", "refresh_token": "refresh", "scopes": []string{"api.connectors.read"},
		"expires_at_unix": time.Now().Add(time.Hour).Unix(),
	}
	data, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutCredential(t.Context(), instance.CredentialRef, data); err != nil {
		t.Fatal(err)
	}
	catalog := withDefaults[providers.CatalogConfig]()
	catalog.URL, catalog.CacheFile = server.URL+"/catalog", filepath.Join(t.TempDir(), "models.dev.json")
	registry, err := providers.NewRegistry(nil, providers.WithConfiguredProviders(provideropenai.NewResolver(
		store, store,
		provideropenai.NewSubscriptionCredentials(store, withDefaults[provideropenai.AuthorizationConfig](), server.Client()),
		providers.NewCatalogSource(catalog, server.Client()), server.Client(),
	)))
	if err != nil {
		t.Fatal(err)
	}
	provider, err := registry.ResolveProvider(t.Context(), instance.ID)
	if err != nil || provider == nil {
		t.Fatalf("ResolveProvider() = %v, %v", provider, err)
	}
}

func TestOpenAIResolverRequiresOpenAIModelMetadata(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"api-key", "chatgpt-subscription"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			store, _ := newResolverSetup(t)
			instance := alfred.ProviderInstance{ID: "openai", Type: "openai", Enabled: true, CredentialRef: "credential"}
			if err := store.PutProviderInstance(t.Context(), instance); err != nil {
				t.Fatal(err)
			}
			credential, err := json.Marshal(map[string]any{
				"version": 1, "provider": "openai", "method": method,
				"fields": map[string]string{"api_key": "test-key"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.PutCredential(t.Context(), instance.CredentialRef, credential); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(writer, `{"other":{"models":{"other-model":{"name":"Other"}}}}`)
			}))
			t.Cleanup(server.Close)
			catalog := withDefaults[providers.CatalogConfig]()
			catalog.URL, catalog.CacheFile = server.URL, filepath.Join(t.TempDir(), "models.dev.json")
			resolver := provideropenai.NewResolver(
				store, store,
				provideropenai.NewSubscriptionCredentials(store, withDefaults[provideropenai.AuthorizationConfig](), server.Client()),
				providers.NewCatalogSource(catalog, server.Client()), server.Client(),
			)
			provider, err := resolver.ResolveProvider(t.Context(), instance.ID)
			if provider != nil || errors.WhatKind(err) != errors.BadGateway ||
				errors.UserMsg(err) != "The model catalog has no OpenAI models." {
				t.Fatalf("ResolveProvider() = %v, %v", provider, err)
			}
		})
	}
}

func newResolverSetup(t *testing.T) (*sqlite.Store, *providers.Submitter) {
	t.Helper()
	store := newAuthorizationStore(t)
	setup, err := providers.NewSetupService(providers.SetupDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return store, providers.NewSubmitter(setup, store, identity.Generator[alfred.CredentialRef]{})
}
