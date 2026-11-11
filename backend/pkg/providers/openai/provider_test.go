package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

const reasoningJSON = `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Inspect the file"}],"encrypted_content":"opaque+/=","future_field":{"keep":"this"}}`

func TestStreamAndReplayOrderedResponse(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]json.RawMessage, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" || request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("unexpected request endpoint, method, or credential")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		writeSSE(t, writer, `{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"Before"}`)
		writeSSE(t, writer, `{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"path\":"}`)
		writeSSE(t, writer, `{"type":"response.output_text.delta","output_index":3,"content_index":0,"delta":"After"}`)
		writeSSE(t, writer, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[`+
			reasoningJSON+`,`+messageJSON("Before")+`,{"type":"function_call","id":"fc_1","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"a.txt\"}","status":"completed"},`+
			messageJSON("After")+`],"usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":5},"output_tokens":10,"output_tokens_details":{"reasoning_tokens":4},"total_tokens":30}}}`)
	}))
	t.Cleanup(server.Close)
	provider := testProvider(server)
	request := testRequest()
	events, err := collect(t.Context(), provider, request)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []alfred.ModelEventKind
	for _, event := range events {
		if event.CallID != request.ID {
			t.Fatalf("call ID = %q", event.CallID)
		}
		kinds = append(kinds, event.Payload.ModelEventKind())
	}
	wantKinds := []alfred.ModelEventKind{"content_delta", "content_delta", "reasoning", "message", "tool_call", "message", "usage", "completed"}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("event kinds = %v, want %v", kinds, wantKinds)
	}
	delta, ok := events[1].Payload.(alfred.ModelContentDelta)
	if !ok || delta.Index != 1 {
		t.Fatalf("second delta = %#v", events[1])
	}
	reasoning, ok := events[2].Payload.(alfred.ModelReasoning)
	if !ok || reasoning.Reasoning.Text != "Inspect the file" || reasoning.Reasoning.TextKind != alfred.ReasoningTextSummary ||
		string(events[2].Native[nativeKey("reasoning")]) != reasoningJSON {
		t.Fatalf("reasoning = %#v", events[2])
	}
	call, ok := events[4].Payload.(alfred.ModelToolCall)
	if !ok || call.Call.ID != "call-1" || call.Call.ToolID != "read" || call.Call.RunID != request.RunID || string(call.Call.Arguments) != `{"path":"a.txt"}` {
		t.Fatalf("call = %#v", events[4])
	}
	usage, ok := events[6].Payload.(alfred.ModelUsage)
	if !ok || usage.Tokens.InputTokens != 20 || usage.Tokens.CachedInputTokens != 5 || usage.Tokens.OutputTokens != 10 || usage.Tokens.ReasoningTokens != 4 {
		t.Fatalf("usage = %#v", events[6])
	}
	completed, ok := events[7].Payload.(alfred.ModelCompleted)
	if !ok || completed.Reason != alfred.ModelFinishReasonToolCall {
		t.Fatalf("completion = %#v", events[7])
	}
	body := <-requests
	assertRequestSettings(t, body)
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(body["input"], &input); err != nil || len(input) != 1 || string(input[0]["role"]) != `"user"` {
		t.Fatalf("initial input = %s, error = %v", body["input"], err)
	}
	before, ok := events[3].Payload.(alfred.ModelMessage)
	if !ok {
		t.Fatal("missing completed message")
	}
	request.Input = append(request.Input,
		alfred.ModelInput{Payload: reasoning.Reasoning, Native: events[2].Native},
		alfred.ModelInput{Payload: before.Message, Native: events[3].Native},
		alfred.ModelInput{Payload: call.Call, Native: events[4].Native},
		alfred.ModelInput{Payload: alfred.ToolResult{CallID: call.Call.ID, IsError: true, Output: []alfred.ContentPart{alfred.TextContent{Text: "file missing"}}}})
	if _, err := collect(t.Context(), provider, request); err != nil {
		t.Fatal(err)
	}
	body = <-requests
	if err := json.Unmarshal(body["input"], &input); err != nil || len(input) != 5 {
		t.Fatalf("follow-up input = %s, error = %v", body["input"], err)
	}
	var expected map[string]json.RawMessage
	if err := json.Unmarshal([]byte(reasoningJSON), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input[1], expected) || string(input[2]["type"]) != `"message"` ||
		string(input[3]["call_id"]) != `"call-1"` || string(input[3]["name"]) != `"read_file"` ||
		string(input[4]["type"]) != `"function_call_output"` || string(input[4]["output"]) != `"Tool error:\nfile missing"` {
		t.Fatalf("follow-up input lost ordered content or continuation: %s", body["input"])
	}
}

func TestHTTPErrorsAreSafeAndNotRetried(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status    int
		kind      errors.Kind
		retryable bool
	}{
		{400, errors.InvalidArgument, false},
		{401, errors.Unauthenticated, true},
		{403, errors.PermissionDenied, false},
		{404, errors.NotFound, false},
		{429, errors.ResourceExhausted, true},
		{500, errors.Unavailable, true},
	} {
		t.Run(strconv.Itoa(test.status), func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = fmt.Fprint(writer, `{"error":{"message":"echo test-key private-prompt","type":"provider_error"}}`)
			}))
			t.Cleanup(server.Close)
			events, err := collect(t.Context(), testProvider(server), testRequest())
			if err == nil || errors.WhatKind(err) != test.kind || errors.IsRetryable(err) != test.retryable || len(events) != 0 || attempts.Load() != 1 {
				t.Fatalf("events=%v error=%v kind=%v retryable=%v attempts=%d", events, err, errors.WhatKind(err), errors.IsRetryable(err), attempts.Load())
			}
			if strings.Contains(err.Error()+errors.UserMsg(err), "test-key") || strings.Contains(err.Error()+errors.UserMsg(err), "private-prompt") {
				t.Fatal("provider error leaked sensitive response content")
			}
		})
	}
}

func TestUnsuccessfulStreamsDoNotEmitCompletedOutput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		terminal string
		kind     errors.Kind
	}{
		{"eof", "", errors.BadGateway},
		{"incomplete", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, errors.BadGateway},
		{"failed", `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"test-key"}}}`, errors.Unavailable},
		{"error", `{"type":"error","code":"rate_limit_exceeded","message":"test-key"}`, errors.ResourceExhausted},
		{"sdk_error", `{"error":{"code":"invalid_api_key","message":"test-key"}}`, errors.Unauthenticated},
		{"malformed", `invalid json`, errors.Unavailable},
		{"unknown_tool", completedJSON(`{"type":"function_call","call_id":"c","name":"unknown","arguments":"{}"}`), errors.BadGateway},
		{"partial_arguments", completedJSON(`{"type":"function_call","call_id":"c","name":"read_file","arguments":"{"}`), errors.BadGateway},
		{"unknown_output", completedJSON(`{"type":"web_search_call"}`), errors.Unimplemented},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeSSE(t, writer, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Partial"}`)
				writeSSE(t, writer, `{"type":"response.output_item.done","item":{"type":"function_call","call_id":"partial","name":"read_file","arguments":"{}"}}`)
				if test.terminal != "" {
					writeSSE(t, writer, test.terminal)
				}
			}))
			t.Cleanup(server.Close)
			events, err := collect(t.Context(), testProvider(server), testRequest())
			if err == nil || errors.WhatKind(err) != test.kind || len(events) != 1 || strings.Contains(err.Error()+errors.UserMsg(err), "test-key") {
				t.Fatalf("events=%v error=%v kind=%v", events, err, errors.WhatKind(err))
			}
		})
	}
}

func TestSubscriptionResponseErrorsAreClassified(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		code      string
		kind      errors.Kind
		retryable bool
	}{
		{"subscription_sharing_usage_limit_exceeded", errors.ResourceExhausted, true},
		{"subscription_sharing_usage_unavailable", errors.Unavailable, true},
		{"subscription_sharing_user_unavailable", errors.Unavailable, true},
		{"subscription_sharing_invalid_user", errors.Unauthenticated, true},
		{"subscription_sharing_user_not_eligible", errors.PermissionDenied, false},
		{"chatpass_v2_scope_not_authorized", errors.PermissionDenied, false},
		{"subscription_sharing_unsupported_capability", errors.InvalidArgument, false},
		{"subscription_sharing_route_not_supported", errors.FailedPrecondition, false},
	} {
		t.Run(test.code, func(t *testing.T) {
			err := responseError(test.code)
			if errors.WhatKind(err) != test.kind || errors.IsRetryable(err) != test.retryable {
				t.Fatalf("responseError(%q) = %v", test.code, err)
			}
		})
	}
}

func TestStreamCancellationAndConsumerStopCloseConnection(t *testing.T) {
	t.Parallel()
	for _, stopEarly := range []bool{false, true} {
		t.Run(strconv.FormatBool(stopEarly), func(t *testing.T) {
			t.Parallel()
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeSSE(t, writer, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hello"}`)
				<-request.Context().Done()
				close(closed)
			}))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var gotError error
			for _, err := range testProvider(server).Stream(ctx, testRequest()) {
				if err != nil {
					gotError = err
					break
				}
				if stopEarly {
					break
				}
				cancel()
			}
			if !stopEarly && !errors.Is(gotError, context.Canceled) {
				t.Fatalf("cancellation error=%v", gotError)
			}
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("stream connection was not closed")
			}
		})
	}
}

func TestServiceUsesExplicitSettings(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-key")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("OPENAI_ORG_ID", "ambient-org")
	t.Setenv("OPENAI_PROJECT_ID", "ambient-project")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Ambient: ambient-value")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-key" || request.Header.Get("OpenAI-Organization") != "" ||
			request.Header.Get("OpenAI-Project") != "" || request.Header.Get("X-Ambient") != "" {
			t.Error("SDK inherited environment settings")
		}
		writeSSE(t, writer, completedJSON(messageJSON("Ready")))
	}))
	t.Cleanup(server.Close)
	events, err := collect(t.Context(), testProvider(server), testRequest())
	if err != nil || len(events) != 2 {
		t.Fatalf("events=%v error=%v", events, err)
	}
}

func testProvider(server *httptest.Server) *Provider {
	config := withDefaults[Config]()
	config.APIKey, config.BaseURL = "test-key", server.URL+"/v1"
	return New("openai", config, []alfred.ModelInfo{{
		Ref: alfred.ModelRef{ModelID: "test-model"}, Capabilities: alfred.ModelCapabilities{Reasoning: true},
	}}, server.Client())
}

func testRequest() alfred.ModelRequest {
	return alfred.ModelRequest{
		ID: "model-call-1", RunID: "run-1", Model: alfred.ModelRef{ProviderID: "openai", ModelID: "test-model"}, Instructions: "Be helpful",
		Input: []alfred.ModelInput{{Payload: alfred.Message{Role: alfred.MessageRoleUser, Content: []alfred.ContentPart{alfred.TextContent{Text: "Read a.txt"}}}}},
		Tools: []alfred.ToolDefinition{{ID: "read", Name: "read_file", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}},
	}
}

func collect(ctx context.Context, provider *Provider, request alfred.ModelRequest) ([]alfred.ModelEvent, error) {
	var events []alfred.ModelEvent
	for event, err := range provider.Stream(ctx, request) {
		if err != nil {
			return events, err
		}
		events = append(events, event)
	}
	return events, nil
}

func writeSSE(t *testing.T, writer http.ResponseWriter, data string) {
	t.Helper()
	writer.Header().Set("Content-Type", "text/event-stream")
	if _, err := fmt.Fprintf(writer, "data: %s\n\n", data); err != nil {
		t.Error(err)
	}
	if err := http.NewResponseController(writer).Flush(); err != nil {
		t.Error(err)
	}
}

func messageJSON(text string) string {
	encoded, _ := json.Marshal(text)
	return `{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + string(encoded) + `,"annotations":[]}]}`
}

func completedJSON(output string) string {
	return `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[` + output + `]}}`
}

func assertRequestSettings(t *testing.T, body map[string]json.RawMessage) {
	t.Helper()
	if string(body["model"]) != `"test-model"` || string(body["instructions"]) != `"Be helpful"` || string(body["store"]) != "false" ||
		string(body["stream"]) != "true" || string(body["include"]) != `["reasoning.encrypted_content"]` || string(body["reasoning"]) != `{"summary":"auto"}` {
		t.Fatalf("unexpected request settings: %v", body)
	}
	if _, present := body["previous_response_id"]; present {
		t.Fatal("stateless requests must not use previous_response_id")
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(body["tools"], &tools); err != nil || len(tools) != 1 || string(tools[0]["name"]) != `"read_file"` || string(tools[0]["strict"]) != "false" {
		t.Fatalf("unexpected tools: %s, error=%v", body["tools"], err)
	}
}
