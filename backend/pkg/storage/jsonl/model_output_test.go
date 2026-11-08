package jsonl

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ArionMiles/alfred"
)

func TestModelCompletionCodecPreservesOrderedAndEmptyOutput(t *testing.T) {
	t.Parallel()
	native := alfred.NativeData{"test.reasoning.v1": json.RawMessage("{\n  \"id\": \"item-1\", \"signature\": \"opaque+/=<&>\", \"extra\": [1, 2]\n}")}
	message := alfred.ModelInput{Payload: alfred.Message{Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "A message"}}}, Native: alfred.NativeData{"test.text.v1": json.RawMessage(`{"phase":"commentary"}`)}}
	call := alfred.ModelInput{Payload: alfred.ToolCall{ID: "call-1", RunID: "run-1", ToolID: "read", Arguments: json.RawMessage(`{}`)}, Native: alfred.NativeData{"test.call.v1": json.RawMessage(`{"signature":"call-proof"}`)}}
	reasoning := alfred.ModelInput{Payload: alfred.Reasoning{Text: "A summary", TextKind: alfred.ReasoningTextSummary}, Native: native}
	opaque := alfred.ModelInput{Payload: alfred.Reasoning{}, Native: native}
	text := alfred.ModelInput{Payload: alfred.Reasoning{Text: "Exposed reasoning", TextKind: alfred.ReasoningTextReasoning}}
	for _, output := range [][]alfred.ModelInput{nil, {}, {message, call, message}, {reasoning, message, call, opaque, text}} {
		payload := alfred.ModelCallCompleted{CallID: "model-call-1", Output: output}
		encoded, err := marshalPayload(payload)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := unmarshalPayload(payload.Kind(), encoded)
		if err != nil || !reflect.DeepEqual(decoded, payload) {
			t.Fatalf("round trip=%#v error=%v want=%#v", decoded, err, payload)
		}
	}
}

func TestModelCompletionCodecRejectsInvalidOutput(t *testing.T) {
	t.Parallel()
	for _, input := range []alfred.ModelInput{
		{Payload: alfred.ToolResult{CallID: "call-1"}},
		{Payload: alfred.Message{Role: alfred.MessageRoleUser}},
		{Payload: alfred.Reasoning{}},
		{Payload: alfred.Reasoning{Text: "Missing kind"}},
		{Payload: alfred.Reasoning{Text: "Wrong kind", TextKind: "unsupported"}},
		{Payload: alfred.Reasoning{}, Native: alfred.NativeData{"": json.RawMessage(`{}`)}},
		{Payload: alfred.Reasoning{}, Native: alfred.NativeData{"test.reasoning.v1": json.RawMessage(`invalid-json`)}},
	} {
		if _, err := marshalPayload(alfred.ModelCallCompleted{Output: []alfred.ModelInput{input}}); err == nil {
			t.Fatalf("accepted invalid output %#v", input)
		}
	}
	for _, data := range []string{
		`{"output":[{"kind":"unknown"}]}`,
		`{"output":[{"kind":"message"}]}`,
		`{"output":[{"kind":"message","message":{"role":"user"}}]}`,
		`{"output":[{"kind":"reasoning"}]}`,
		`{"output":[{"kind":"reasoning","reasoning":{}}]}`,
		`{"output":[{"kind":"reasoning","reasoning":{"text":"Missing kind"}}]}`,
		`{"output":[{"kind":"reasoning","reasoning":{"text":"x","text_kind":"unsupported"}}]}`,
		`{"output":[{"kind":"reasoning","reasoning":{"text":"x","text_kind":"summary"},"tool_call":{}}]}`,
		`{"output":[{"kind":"reasoning","reasoning":{},"native":{"test":"aW52YWxpZA=="}}]}`,
	} {
		if _, err := unmarshalPayload(alfred.RunEventKindModelCallCompleted, []byte(data)); err == nil {
			t.Fatalf("accepted invalid model output %s", data)
		}
	}
}

func TestVersionOneReasoningDataIsLiftedIntoNative(t *testing.T) {
	t.Parallel()
	data := []byte("{\n \"signature\": \"opaque+/=\"\n}")
	encoded := []byte(`{"version":1,"kind":"model_call_completed","payload":{"call_id":"c","output":[{"kind":"reasoning","reasoning":{"continuation":{"format":"openai.responses.reasoning.v1","data":"` + base64.StdEncoding.EncodeToString(data) + `"}}}]}}`)
	event, err := unmarshalEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	completed, ok := event.Payload.(alfred.ModelCallCompleted)
	if !ok || len(completed.Output) != 1 || string(completed.Output[0].Native["openai.responses.reasoning.v1"]) != string(data) {
		t.Fatalf("legacy output=%#v", event.Payload)
	}
	rewritten, err := marshalEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	var record eventRecord
	if err := json.Unmarshal(rewritten, &record); err != nil {
		t.Fatal(err)
	}
	if record.Version != 2 {
		t.Fatalf("written version=%d", record.Version)
	}
	again, err := unmarshalEvent(rewritten)
	if err != nil || !reflect.DeepEqual(event, again) {
		t.Fatalf("rewritten event=%#v error=%v", again, err)
	}
}

func TestNativePlacementIsIndependentOfProviderAndPayloadKind(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		key     string
		data    string
		payload alfred.ModelInputPayload
	}{
		{"openai.responses.message.v1", `{"id":"msg_1","phase":"commentary"}`, alfred.Message{Role: alfred.MessageRoleAssistant}},
		{"anthropic.messages.thinking.v1", `{"signature":"thinking-proof","extra":true}`, alfred.Reasoning{}},
		{"google.gemini.part.v1", `{"functionCall":{"name":"read_file","args":{"path":"a.txt"}},"thoughtSignature":"call-proof"}`, alfred.ToolCall{ID: "c", ToolID: "read", Arguments: json.RawMessage(`{"path":"a.txt"}`)}},
		{"deepseek.chat.reasoning.v1", `{"reasoning_content":"Inspect the file"}`, alfred.Reasoning{Text: "Inspect the file", TextKind: alfred.ReasoningTextReasoning}},
	} {
		t.Run(test.key, func(t *testing.T) {
			t.Parallel()
			want := alfred.ModelCallCompleted{Output: []alfred.ModelInput{{Payload: test.payload, Native: alfred.NativeData{test.key: json.RawMessage(test.data)}}}}
			data, err := marshalPayload(want)
			if err != nil {
				t.Fatal(err)
			}
			got, err := unmarshalPayload(want.Kind(), data)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("native round trip=%#v error=%v", got, err)
			}
		})
	}
}
