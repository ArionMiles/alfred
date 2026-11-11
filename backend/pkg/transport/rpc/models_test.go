package rpc

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/creasty/defaults"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/identity"
	"github.com/ArionMiles/alfred/pkg/invocation"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/providers/openai"
	"github.com/ArionMiles/alfred/pkg/storage/jsonl"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
)

func TestModelRPCListsValidatesAndPreservesSelectedPreference(t *testing.T) {
	t.Parallel()
	clients, store := newModelTestClients(t)
	initial, err := clients.Models.GetSelectedModel(t.Context(), &alfredv1.GetSelectedModelRequest{})
	if err != nil || initial.GetModel() != nil {
		t.Fatalf("initial selection=%v error=%v", initial, err)
	}
	choices, err := clients.Models.ListModels(t.Context(), &alfredv1.ListModelsRequest{})
	if err != nil || len(choices.GetModels()) != 2 {
		t.Fatalf("choices=%v error=%v", choices, err)
	}
	first := choices.GetModels()[0]
	if first.GetName() != "Alpha" || first.GetProviderName() != "OpenAI" || first.GetRef().GetProviderId() != "openai" ||
		first.GetCapabilities().GetContextWindow() != 128000 || !first.GetCapabilities().GetReasoning() || first.GetCapabilities().GetInputModalities()[0] != alfredv1.ContentKind_CONTENT_KIND_TEXT {
		t.Fatalf("model metadata=%v", first)
	}
	instance, err := store.GetProviderInstance(t.Context(), "openai")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(choices.String(), "test-model-secret") || strings.Contains(choices.String(), string(instance.CredentialRef)) {
		t.Fatal("model discovery exposed credentials")
	}
	selected, err := clients.Models.SelectModel(t.Context(), &alfredv1.SelectModelRequest{Model: first.GetRef()})
	if err != nil || !proto.Equal(selected.GetModel(), first.GetRef()) {
		t.Fatalf("selection=%v error=%v", selected, err)
	}
	for _, test := range []struct {
		ref  *alfredv1.ModelRef
		code connect.Code
	}{
		{nil, connect.CodeInvalidArgument},
		{&alfredv1.ModelRef{ProviderId: "openai", ModelId: "missing"}, connect.CodeNotFound},
		{&alfredv1.ModelRef{ProviderId: "openai", ModelId: "image"}, connect.CodeUnimplemented},
		{&alfredv1.ModelRef{ProviderId: "disabled", ModelId: "a"}, connect.CodeFailedPrecondition},
		{&alfredv1.ModelRef{ProviderId: "deepseek", ModelId: "a"}, connect.CodeUnimplemented},
	} {
		if _, err := clients.Models.SelectModel(t.Context(), &alfredv1.SelectModelRequest{Model: test.ref}); connect.CodeOf(err) != test.code {
			t.Fatalf("invalid selection error=%v want=%v", err, test.code)
		}
	}
	after, err := clients.Models.GetSelectedModel(t.Context(), &alfredv1.GetSelectedModelRequest{})
	if err != nil || !proto.Equal(after.GetModel(), first.GetRef()) {
		t.Fatalf("failed selection changed preference=%v error=%v", after, err)
	}
}

func TestNewRunsSnapshotSelectionWithoutChangingEarlierRuns(t *testing.T) {
	t.Parallel()
	clients, store := newModelTestClients(t)
	for _, id := range []string{"a", "z"} {
		_, err := clients.Models.SelectModel(t.Context(), &alfredv1.SelectModelRequest{Model: &alfredv1.ModelRef{ProviderId: "openai", ModelId: id}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = clients.Invocation.Invoke(t.Context(), &alfredv1.InvokeRequest{ProjectId: "project-1", Source: alfredv1.InvocationSource_INVOCATION_SOURCE_USER, Input: []*alfredv1.ContentPart{{Value: &alfredv1.ContentPart_Text{Text: "Hello"}}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := store.ListSessions(t.Context(), "project-1", alfred.SessionListOptions{Limit: 10})
	if err != nil || len(sessions.Sessions) != 2 {
		t.Fatalf("sessions=%v error=%v", sessions, err)
	}
	var foundA, foundZ bool
	for _, session := range sessions.Sessions {
		runs, err := store.ListRuns(t.Context(), session.ID, alfred.RunListOptions{Limit: 10})
		if err != nil || len(runs.Runs) != 1 {
			t.Fatalf("runs=%v error=%v", runs, err)
		}
		model := runs.Runs[0].DefaultModel
		foundA = foundA || model.ModelID == "a"
		foundZ = foundZ || model.ModelID == "z"
		if model.ProviderID != "openai" {
			t.Fatalf("run provider=%q", model.ProviderID)
		}
	}
	if !foundA || !foundZ {
		t.Fatal("changing selection rewrote an earlier run or was not read for the next run")
	}
}

func TestModelHandlerRejectsNilRequests(t *testing.T) {
	t.Parallel()
	handler := NewModelHandler(nil)
	_, a := handler.ListModels(t.Context(), nil)
	_, b := handler.GetSelectedModel(t.Context(), nil)
	_, c := handler.SelectModel(t.Context(), nil)
	for _, err := range []error{a, b, c} {
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("nil request error=%v", err)
		}
	}
}

func newModelTestClients(t *testing.T) (Clients, *sqlite.Store) {
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
	if err := store.CreateProject(t.Context(), alfred.Project{ID: "project-1", Root: t.TempDir(), SkillTrust: alfred.ProjectSkillTrustUnknown, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/catalog" {
			t.Error("model discovery or selection made an inference request")
		}
		_, _ = fmt.Fprint(writer, `{"openai":{"models":{
   "z":{"name":"Zulu","modalities":{"input":["text"],"output":["text"]}},
   "a":{"name":"Alpha","reasoning":true,"tool_call":true,"limit":{"context":128000,"output":16000},"modalities":{"input":["text","image"],"output":["text"]}},
   "image":{"name":"Image only","modalities":{"input":["text"],"output":["image"]}}
  }}}`)
	}))
	t.Cleanup(source.Close)
	setup, err := providers.NewSetupService(providers.SetupDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	submitter := providers.NewSubmitter(setup, store, identity.Generator[alfred.CredentialRef]{})
	if _, err := submitter.Submit(t.Context(), store, alfred.ProviderSetupSubmission{
		Type: "openai", MethodID: "api-key", Values: map[string]string{"api_key": "test-model-secret"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, instance := range []alfred.ProviderInstance{{ID: "disabled", Type: "openai"}, {ID: "deepseek", Type: "deepseek", Enabled: true}} {
		if err := store.PutProviderInstance(t.Context(), instance); err != nil {
			t.Fatal(err)
		}
	}
	var catalog providers.CatalogConfig
	defaults.MustSet(&catalog)
	catalog.URL, catalog.CacheFile = source.URL+"/catalog", filepath.Join(t.TempDir(), "models.dev.json")
	registry, err := providers.NewRegistry(nil, providers.WithConfiguredProviders(openai.NewResolver(
		store, store, nil, providers.NewCatalogSource(catalog, source.Client()), source.Client(),
	)))
	if err != nil {
		t.Fatal(err)
	}
	modelService := providers.NewModelService(store, registry, store)
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	dispatcher.EXPECT().Dispatch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	events := jsonl.New(jsonl.Config{DataDir: t.TempDir(), LockShardCount: 64}, store, store, identity.Generator[alfred.RunEventID]{})
	invocations := invocation.NewService(invocation.Dependencies{
		ModelSelection: store, Projects: store, Sessions: store, Invocations: store, Runs: store,
		InvocationIDGenerator: identity.Generator[alfred.InvocationID]{}, SessionIDGenerator: identity.Generator[alfred.SessionID]{}, RunIDGenerator: identity.Generator[alfred.RunID]{},
		Dispatcher: dispatcher, Events: events,
	})
	server := httptest.NewServer(NewServer(Handlers{
		Models: NewModelHandler(modelService), Invocation: NewInvocationHandler(invocations),
		Daemon: alfredv1connect.UnimplementedDaemonServiceHandler{}, Project: alfredv1connect.UnimplementedProjectServiceHandler{},
		Session: alfredv1connect.UnimplementedSessionServiceHandler{}, Run: alfredv1connect.UnimplementedRunServiceHandler{}, ProviderSetup: NewProviderSetupHandler(setup, submitter, store, nil),
	}))
	t.Cleanup(server.Close)
	return NewClients(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL))), store
}
