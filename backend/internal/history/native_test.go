package history

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ArionMiles/alfred"
)

func TestNativeDataStaysAttachedDuringPendingCallFiltering(t *testing.T) {
	t.Parallel()
	message := alfred.ModelInput{
		Payload: alfred.Message{Role: alfred.MessageRoleAssistant},
		Native:  alfred.NativeData{"example.text.v1": json.RawMessage(`{"signature":"text-proof"}`)},
	}
	call := alfred.ToolCall{ID: "call-1", ToolID: "read"}
	tool := alfred.ModelInput{
		Payload: call,
		Native:  alfred.NativeData{"example.call.v1": json.RawMessage(`{"signature":"call-proof"}`)},
	}
	projection := New(nil)
	projection.Apply(alfred.RunEvent{Payload: alfred.ModelCallCompleted{CallID: "model-call-1", Output: []alfred.ModelInput{message, tool}}})
	if got := projection.ResolvedInput(); !reflect.DeepEqual(got, []alfred.ModelInput{message}) {
		t.Fatalf("resolved input=%#v", got)
	}
	if got := projection.PendingCalls(); !reflect.DeepEqual(got, []alfred.ToolCall{call}) {
		t.Fatalf("pending calls=%#v", got)
	}
	result := alfred.ToolResult{CallID: call.ID}
	projection.Apply(alfred.RunEvent{Payload: alfred.ToolCallCompleted{Result: result}})
	want := []alfred.ModelInput{message, tool, {Payload: result}}
	if got := projection.ResolvedInput(); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved exchange=%#v", got)
	}
}
