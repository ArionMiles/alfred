package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/tools"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestSearchSetupIsSeparateFromModelProvidersAndFeedsExa(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(t.Context(), sqlite.Config{Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	setup := tools.NewSearchSetup(tools.SearchSetupDefinitions(), store, store)
	server := connect.NewServer()
	alfredv1connect.RegisterSearchSetupServiceHandler(server, NewSearchSetupHandler(setup))
	modelSetup, err := providers.NewSetupService(providers.SetupDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	alfredv1connect.RegisterProviderSetupServiceHandler(server, NewProviderSetupHandler(modelSetup, providers.NewSubmitter(modelSetup, store), store, nil))
	clients := NewClients(connect.NewClient(connectinprocess.New(server)))
	definitions, err := clients.ProviderSetup.ListProviderDefinitions(t.Context(), &alfredv1.ListProviderDefinitionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions.GetDefinitions() {
		if definition.GetType() == "exa" {
			t.Fatal("search backend appeared in model Providers")
		}
	}
	searches, err := clients.SearchSetup.ListSearchBackends(t.Context(), &alfredv1.ListSearchBackendsRequest{})
	if err != nil || len(searches.GetBackends()) != 1 || searches.GetBackends()[0].GetId() != "exa" {
		t.Fatalf("backends=%v err=%v", searches, err)
	}
	invalid, err := clients.SearchSetup.SaveSearchSetup(t.Context(), &alfredv1.SaveSearchSetupRequest{Backend: "exa"})
	if err != nil || invalid.GetSaved() || len(invalid.GetFieldErrors()) != 1 {
		t.Fatalf("validation=%v err=%v", invalid, err)
	}
	for _, key := range []string{"first-secret", "replacement-secret"} {
		previous, _ := store.GetSearchSettings(t.Context(), "exa")
		saved, err := clients.SearchSetup.SaveSearchSetup(t.Context(), &alfredv1.SaveSearchSetupRequest{Backend: "exa", Values: map[string]string{"api_key": key}})
		if err != nil || !saved.GetSaved() {
			t.Fatalf("save=%v err=%v", saved, err)
		}
		prepared, err := clients.SearchSetup.PrepareSearchSetup(t.Context(), &alfredv1.PrepareSearchSetupRequest{Backend: "exa"})
		if err != nil || prepared.GetValues()["api_key"] != "" {
			t.Fatal("saved key was prefilled")
		}
		encoded, _ := protojson.Marshal(prepared)
		if strings.Contains(string(encoded), key) {
			t.Fatal("secret exposed in form")
		}
		settings, err := store.GetSearchSettings(t.Context(), "exa")
		if err != nil || settings.CredentialRef == "" || !settings.Enabled {
			t.Fatalf("settings=%v err=%v", settings, err)
		}
		if _, err := store.GetProviderInstance(t.Context(), "exa"); errors.WhatKind(err) != errors.NotFound {
			t.Fatal("search setup wrote a model-provider record")
		}
		if previous.CredentialRef != "" {
			if _, err := store.GetCredential(t.Context(), previous.CredentialRef); errors.WhatKind(err) != errors.NotFound {
				t.Fatal("previous key retained after successful replacement")
			}
		}
		remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("x-api-key") != key {
				t.Error("runtime did not use the search credential")
			}
			_, _ = writer.Write([]byte(`{"results":[{"title":"Search result","url":"https://example.org"}]}`))
		}))
		results, err := tools.NewExa(tools.ExaConfig{Endpoint: remote.URL}, store, store, tools.WithHTTPClient(remote.Client())).Search(t.Context(), "query", 1)
		remote.Close()
		if err != nil || len(results) != 1 {
			t.Fatalf("search=%v err=%v", results, err)
		}
	}
	_, err = clients.SearchSetup.PrepareSearchSetup(t.Context(), &alfredv1.PrepareSearchSetupRequest{Backend: "openai"})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("model backend accepted by search setup")
	}
}

func TestSearchFormPrefillsOnlyNonSecretFields(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(t.Context(), sqlite.Config{Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	definition := alfred.SearchBackendDefinition{ID: "search-test", Fields: []alfred.SetupField{
		{ID: "api_key", Kind: alfred.SetupFieldSecret, Required: true}, {ID: "endpoint", Kind: alfred.SetupFieldText},
	}}
	setup := tools.NewSearchSetup([]alfred.SearchBackendDefinition{definition}, store, store)
	if problems, err := setup.Save(t.Context(), definition.ID, map[string]string{"api_key": "secret", "endpoint": "https://example.org"}); err != nil || len(problems) != 0 {
		t.Fatalf("save=%v err=%v", problems, err)
	}
	form, err := setup.Prepare(t.Context(), definition.ID)
	if err != nil || form.Values["api_key"] != "" || form.Values["endpoint"] != "https://example.org" {
		t.Fatalf("form=%v err=%v", form, err)
	}
	settings, err := store.GetSearchSettings(t.Context(), definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.GetCredential(t.Context(), settings.CredentialRef)
	var credential struct {
		Backend string            `json:"backend"`
		Fields  map[string]string `json:"fields"`
	}
	if err != nil || json.Unmarshal(data, &credential) != nil || credential.Backend != definition.ID || credential.Fields["api_key"] != "secret" {
		t.Fatal("search-specific credential format not saved")
	}
}
