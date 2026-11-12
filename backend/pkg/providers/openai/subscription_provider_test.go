package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestSubscriptionProviderUsesAccountModelsAndNamespacedTools(t *testing.T) {
	t.Parallel()
	store := newSubscriptionCredentialStore(t)
	now := time.Now().Truncate(time.Second)
	ref := seedSubscriptionCredential(t, store, oauthCredentialRecord{
		AccessToken: "subscription-access", RefreshToken: "subscription-refresh", Scopes: []string{requiredDirectScope},
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	requests := make(chan map[string]json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer subscription-access" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(writer).Encode(map[string]any{"models": []map[string]string{
				{"slug": "second-model", "display_name": "Second for this account", "visibility": "list"},
				{"slug": "hidden-model", "display_name": "Hidden", "visibility": "hidden"},
				{"slug": "test-model", "display_name": "Test for this account", "visibility": "list"},
				{"slug": "unknown-model", "display_name": "Unknown", "visibility": "list"},
			}})
		case "/v1/responses":
			var body map[string]json.RawMessage
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			requests <- body
			writeSSE(t, writer, completedJSON(messageJSON("Ready")))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	config := withDefaults[Config]()
	config.BaseURL = server.URL + "/v1"
	manager := NewSubscriptionCredentials(store, withDefaults[AuthorizationConfig](), WithHTTPClient(server.Client()))
	provider := NewSubscription("openai", config, []alfred.ModelInfo{
		{Ref: alfred.ModelRef{ModelID: "test-model"}, Name: "Catalog test", Capabilities: alfred.ModelCapabilities{Reasoning: true}},
		{Ref: alfred.ModelRef{ModelID: "second-model"}, Name: "Catalog second"},
		{Ref: alfred.ModelRef{ModelID: "hidden-model"}, Name: "Catalog hidden"},
	}, SubscriptionDependencies{Credentials: manager, CredentialRef: ref}, WithHTTPClient(server.Client()))

	models, err := provider.Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("Models() = %#v", models)
	}
	got := []alfred.ModelID{models[0].Ref.ModelID, models[1].Ref.ModelID}
	if !reflect.DeepEqual(got, []alfred.ModelID{"second-model", "test-model"}) ||
		models[0].Name != "Second for this account" || models[1].Name != "Test for this account" {
		t.Fatalf("Models() = %#v", models)
	}
	if _, err := collect(t.Context(), provider, testRequest()); err != nil {
		t.Fatal(err)
	}
	body := <-requests
	assertSubscriptionRequest(t, body)
}

func assertSubscriptionRequest(t *testing.T, body map[string]json.RawMessage) {
	t.Helper()
	if string(body["store"]) != "false" || string(body["stream"]) != "true" {
		t.Fatalf("request settings = %v", body)
	}
	if _, present := body["previous_response_id"]; present {
		t.Fatal("stateless subscription request used previous_response_id")
	}
	var tools []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Tools []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body["tools"], &tools); err != nil || len(tools) != 1 || tools[0].Type != "namespace" ||
		tools[0].Name != "alfred" || len(tools[0].Tools) != 1 || tools[0].Tools[0].Type != "function" ||
		tools[0].Tools[0].Name != "read_file" {
		t.Fatalf("subscription tools = %s, error = %v", body["tools"], err)
	}
}

func TestSubscriptionReasoningAndToolResultContinuation(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]json.RawMessage, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer subscription-access" {
			t.Error("missing subscription bearer")
		}
		if request.URL.Path == "/v1/models" {
			_, _ = writer.Write([]byte(`{"models":[{"slug":"test-model","visibility":"list"}]}`))
			return
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		writeSSE(t, writer, `{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Inspect"}`)
		writeSSE(t, writer, completedJSON(reasoningJSON+`,{"type":"function_call","id":"fc_1","call_id":"call-1","name":"read_file","namespace":"alfred","arguments":"{}","status":"completed"}`))
	}))
	t.Cleanup(server.Close)
	provider := subscriptionTestProvider(t, server)
	request := testRequest()
	events, err := collect(t.Context(), provider, request)
	if err != nil || len(events) != 4 {
		t.Fatalf("events = %#v, error = %v", events, err)
	}
	if _, ok := events[0].Payload.(alfred.ModelReasoningDelta); !ok {
		t.Fatalf("missing reasoning delta: %#v", events[0])
	}
	reasoning, ok := events[1].Payload.(alfred.ModelReasoning)
	if !ok || reasoning.Reasoning.Text != "Inspect the file" {
		t.Fatalf("reasoning = %#v", events[1])
	}
	call, ok := events[2].Payload.(alfred.ModelToolCall)
	if !ok || call.Call.ToolID != "read" || call.Call.ID != "call-1" {
		t.Fatalf("tool call = %#v", events[2])
	}
	assertSubscriptionRequest(t, <-requests)
	request.Input = append(request.Input,
		alfred.ModelInput{Payload: reasoning.Reasoning, Native: events[1].Native},
		alfred.ModelInput{Payload: call.Call, Native: events[2].Native},
		alfred.ModelInput{Payload: alfred.ToolResult{CallID: call.Call.ID, Output: []alfred.ContentPart{alfred.TextContent{Text: "file contents"}}}},
	)
	if _, err := collect(t.Context(), provider, request); err != nil {
		t.Fatal(err)
	}
	body := <-requests
	assertSubscriptionRequest(t, body)
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(body["input"], &input); err != nil || len(input) != 4 {
		t.Fatalf("input = %s, error = %v", body["input"], err)
	}
	var expectedReasoning map[string]json.RawMessage
	if err := json.Unmarshal([]byte(reasoningJSON), &expectedReasoning); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input[1], expectedReasoning) || string(input[2]["namespace"]) != `"alfred"` ||
		string(input[2]["call_id"]) != `"call-1"` || string(input[3]["type"]) != `"function_call_output"` ||
		string(input[3]["output"]) != `"file contents"` {
		t.Fatalf("lost native reasoning, namespace, or tool result: %s", body["input"])
	}
}

func TestSubscriptionFailuresPreserveRecoveryAndNeverCompletePartialOutput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		code    string
		status  int
		kind    errors.Kind
		message string
	}{
		{"subscription_sharing_usage_limit_exceeded", 429, errors.ResourceExhausted, "https://chatgpt.com/settings/usage"},
		{"subscription_sharing_usage_unavailable", 503, errors.Unavailable, "Wait and try again"},
		{"subscription_sharing_user_not_eligible", 403, errors.PermissionDenied, "account or workspace policy"},
		{"subscription_sharing_unsupported_capability", 400, errors.InvalidArgument, "Change the request or model"},
	} {
		for _, phase := range []string{"models", "http", "stream"} {
			t.Run(test.code+"/"+phase, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if request.URL.Path == "/v1/models" && phase != "models" {
						_, _ = writer.Write([]byte(`{"models":[{"slug":"test-model","visibility":"list"}]}`))
						return
					}
					if phase != "stream" {
						writer.Header().Set("Content-Type", "application/json")
						writer.WriteHeader(test.status)
						_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
							"code": test.code, "message": "subscription-access private-prompt", "type": "provider_error",
						}})
						return
					}
					writeSSE(t, writer, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Partial"}`)
					writeSSE(t, writer, `{"type":"response.failed","response":{"status":"failed","error":{"code":"`+test.code+`","message":"private-prompt"}}}`)
				}))
				t.Cleanup(server.Close)
				provider := subscriptionTestProvider(t, server)
				events, err := collect(t.Context(), provider, testRequest())
				if errors.WhatKind(err) != test.kind || !strings.Contains(errors.UserMsg(err), test.message) {
					t.Fatalf("error = %v, user message = %q", err, errors.UserMsg(err))
				}
				wantEvents := 0
				if phase == "stream" {
					wantEvents = 1
				}
				if len(events) != wantEvents {
					t.Fatalf("unexpected completed output: %#v", events)
				}
				for _, event := range events {
					if _, ok := event.Payload.(alfred.ModelContentDelta); !ok {
						t.Fatalf("failure promoted partial output: %#v", event)
					}
				}
				if strings.Contains(err.Error()+errors.UserMsg(err), "subscription-access") || strings.Contains(err.Error()+errors.UserMsg(err), "private-prompt") {
					t.Fatal("provider error exposed sensitive content")
				}
			})
		}
	}
}

func subscriptionTestProvider(t *testing.T, server *httptest.Server) *Provider {
	t.Helper()
	store := newSubscriptionCredentialStore(t)
	now := time.Now().Truncate(time.Second)
	ref := seedSubscriptionCredential(t, store, oauthCredentialRecord{
		AccessToken: "subscription-access", RefreshToken: "subscription-refresh", Scopes: []string{requiredDirectScope},
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	config := withDefaults[Config]()
	config.BaseURL = server.URL + "/v1"
	return NewSubscription("openai", config, []alfred.ModelInfo{{
		Ref: alfred.ModelRef{ModelID: "test-model"}, Capabilities: alfred.ModelCapabilities{Reasoning: true},
	}}, SubscriptionDependencies{
		Credentials:   NewSubscriptionCredentials(store, withDefaults[AuthorizationConfig](), WithHTTPClient(server.Client())),
		CredentialRef: ref,
	}, WithHTTPClient(server.Client()))
}
