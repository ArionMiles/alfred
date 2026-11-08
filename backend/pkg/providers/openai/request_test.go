package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestUnsupportedInputFailsBeforeSending(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input alfred.ModelInput
		kind  errors.Kind
	}{
		{"resource", alfred.ModelInput{Payload: alfred.Message{Role: alfred.MessageRoleUser, Content: []alfred.ContentPart{alfred.ResourceContent{Type: alfred.ContentKindImage}}}}, errors.Unimplemented},
		{"foreign_native", alfred.ModelInput{Payload: alfred.Reasoning{}, Native: alfred.NativeData{"other.reasoning.v1": json.RawMessage(`{}`)}}, errors.Unimplemented},
		{"readable_reasoning_only", alfred.ModelInput{Payload: alfred.Reasoning{Text: "summary", TextKind: alfred.ReasoningTextSummary}}, errors.Unimplemented},
		{"invalid_native", alfred.ModelInput{Payload: alfred.Reasoning{}, Native: alfred.NativeData{nativeKey("reasoning"): json.RawMessage(`{"type":"message"}`)}}, errors.InvalidInput},
		{"removed_tool", alfred.ModelInput{Payload: alfred.ToolCall{ID: "c", ToolID: "unavailable", Arguments: json.RawMessage(`{}`)}}, errors.NotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("unsupported input reached the network")
			}))
			t.Cleanup(server.Close)
			request := testRequest()
			request.Input = []alfred.ModelInput{test.input}
			events, err := collect(t.Context(), testProvider(server), request)
			if errors.WhatKind(err) != test.kind || len(events) != 0 {
				t.Fatalf("events=%v error=%v", events, err)
			}
		})
	}
}

func TestModelsUseCallerMetadata(t *testing.T) {
	t.Parallel()
	models := []alfred.ModelInfo{
		{Ref: alfred.ModelRef{ModelID: "z"}, Name: "Z", Capabilities: alfred.ModelCapabilities{InputModalities: []alfred.ContentKind{alfred.ContentKindText}}},
		{Ref: alfred.ModelRef{ModelID: "a"}, Name: "A"},
	}
	provider := New("native", DefaultConfig(), models, http.DefaultClient)
	models[0].Capabilities.InputModalities[0] = alfred.ContentKindImage
	listed, err := provider.Models(t.Context())
	if err != nil || len(listed) != 2 || listed[0].Ref.ModelID != "a" || listed[1].Ref.ProviderID != "native" || provider.ID() != "native" {
		t.Fatalf("models=%v error=%v", listed, err)
	}
	listed[1].Capabilities.InputModalities[0] = alfred.ContentKindVideo
	resolved, err := provider.ResolveModel(t.Context(), "z")
	if err != nil || resolved.Name != "Z" || resolved.Capabilities.InputModalities[0] != alfred.ContentKindText {
		t.Fatalf("resolved=%v error=%v", resolved, err)
	}
	if _, err := provider.ResolveModel(t.Context(), "unknown"); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("unknown model error=%v", err)
	}
}

func TestReasoningAndRefusalOutput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		output string
		text   string
		kind   alfred.ReasoningTextKind
	}{
		{"opaque", `{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque"}`, "", ""},
		{"exposed", `{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"Exposed reasoning"}],"encrypted_content":"opaque"}`, "Exposed reasoning", alfred.ReasoningTextReasoning},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeSSE(t, writer, completedJSON(test.output+`,{"type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"Cannot help"}]}`))
			}))
			t.Cleanup(server.Close)
			events, err := collect(t.Context(), testProvider(server), testRequest())
			if err != nil || len(events) != 3 {
				t.Fatalf("events=%v error=%v", events, err)
			}
			reasoning, ok := events[0].Payload.(alfred.ModelReasoning)
			if !ok || reasoning.Reasoning.Text != test.text || reasoning.Reasoning.TextKind != test.kind || events[0].Native[nativeKey("reasoning")] == nil {
				t.Fatalf("reasoning=%#v", events[0])
			}
			message, ok := events[1].Payload.(alfred.ModelMessage)
			if !ok || len(message.Message.Content) != 1 || message.Message.Content[0] != (alfred.TextContent{Text: "Cannot help"}) {
				t.Fatalf("refusal=%#v", events[1])
			}
		})
	}
}

func TestNativeMessagePhaseAndEmptyTextSurviveReplay(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		phase string
		text  string
	}{
		{"commentary", "Inspecting the file"}, {"final_answer", "Finished"}, {"commentary", ""},
	} {
		t.Run(test.phase+"/"+test.text, func(t *testing.T) {
			t.Parallel()
			bodies := make(chan map[string]json.RawMessage, 2)
			output := strings.Replace(messageJSON(test.text), `"role":"assistant"`, `"role":"assistant","phase":"`+test.phase+`"`, 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				bodies <- body
				writeSSE(t, writer, completedJSON(output))
			}))
			t.Cleanup(server.Close)
			provider := testProvider(server)
			request := testRequest()
			events, err := collect(t.Context(), provider, request)
			if err != nil || len(events) != 2 {
				t.Fatalf("events=%v error=%v", events, err)
			}
			<-bodies
			message, ok := events[0].Payload.(alfred.ModelMessage)
			if !ok || string(events[0].Native[nativeKey("message")]) != output {
				t.Fatalf("native message=%#v", events[0])
			}
			request.Input = append(request.Input, alfred.ModelInput{Payload: message.Message, Native: events[0].Native})
			if _, err := collect(t.Context(), provider, request); err != nil {
				t.Fatal(err)
			}
			body := <-bodies
			var input []json.RawMessage
			if err := json.Unmarshal(body["input"], &input); err != nil || len(input) != 2 {
				t.Fatalf("input=%s error=%v", body["input"], err)
			}
			if string(input[1]) != output {
				t.Fatalf("replayed message=%s want=%s", input[1], output)
			}
		})
	}
}
