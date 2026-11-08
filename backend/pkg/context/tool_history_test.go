package context

import (
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
)

func TestBuilderIncludesResolvedToolExchanges(t *testing.T) {
	t.Parallel()
	call := alfred.ToolCall{ID: "call-1", RunID: "earlier", ToolID: "read"}
	result := alfred.ToolResult{CallID: call.ID, Output: []alfred.ContentPart{alfred.TextContent{Text: "file contents"}}}
	requested := alfred.RunEvent{Payload: alfred.ToolCallRequested{ModelCallID: "model-call-1", Call: call}}
	completed := alfred.RunEvent{Payload: alfred.ModelCallCompleted{CallID: "model-call-1"}}
	for _, test := range []struct {
		name   string
		events []alfred.RunEvent
		want   []alfred.ModelInput
	}{
		{
			name:   "completed exchange",
			events: []alfred.RunEvent{requested, completed, {Payload: alfred.ToolCallCompleted{Result: result}}},
			want:   []alfred.ModelInput{{Payload: call}, {Payload: result}},
		},
		{
			name:   "incomplete model call",
			events: []alfred.RunEvent{requested, {Payload: alfred.ToolCallCompleted{Result: result}}},
		},
		{
			name:   "unresolved tool call",
			events: []alfred.RunEvent{requested, completed},
		},
		{
			name: "denied approval",
			events: []alfred.RunEvent{
				requested, completed,
				{Payload: alfred.ApprovalRequested{Request: alfred.ApprovalRequest{ID: "approval-1", CallID: call.ID}}},
				{Payload: alfred.ApprovalResolved{Resolution: alfred.ApprovalResolution{ApprovalID: "approval-1", Outcome: alfred.ApprovalOutcomeDenied}}},
			},
			want: []alfred.ModelInput{{Payload: call}, {Payload: alfred.ToolResult{
				CallID: call.ID, IsError: true, Output: []alfred.ContentPart{alfred.TextContent{Text: "Tool execution was denied by the user."}},
			}}},
		},
		{
			name: "tool requested input",
			events: []alfred.RunEvent{
				requested, completed,
				{Payload: alfred.InputRequested{Request: alfred.InputRequest{ID: "input-1", CallID: call.ID}}},
				{Payload: alfred.InputReceived{Response: alfred.InputResponse{RequestID: "input-1", Content: result.Output}}},
			},
			want: []alfred.ModelInput{{Payload: call}, {Payload: result}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			controller := gomock.NewController(t)
			catalog := mocks.NewMockToolCatalog(controller)
			catalog.EXPECT().ListTools(gomock.Any()).Return(nil, nil)
			history := mocks.NewMockRunEventReader(controller)
			history.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("session-1")).Return(messageHistory(test.events))
			built, err := New(Config{}, catalog, history, testProjects(t)).Build(t.Context(), alfred.ContextRequest{SessionID: "session-1", RunID: "current"})
			if err != nil {
				t.Fatal(err)
			}
			if len(built.Input) != len(test.want) || (len(test.want) > 0 && !reflect.DeepEqual(built.Input, test.want)) {
				t.Fatalf("history input = %#v, want %#v", built.Input, test.want)
			}
		})
	}
}

func TestBuilderPreservesToolCallAndResultOrder(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	catalog := mocks.NewMockToolCatalog(controller)
	catalog.EXPECT().ListTools(gomock.Any()).Return(nil, nil)
	first := alfred.ToolCall{ID: "first", ToolID: "read"}
	second := alfred.ToolCall{ID: "second", ToolID: "read"}
	firstResult := alfred.ToolResult{CallID: first.ID}
	secondResult := alfred.ToolResult{CallID: second.ID}
	message := alfred.Message{Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Checking both files"}}}
	history := mocks.NewMockRunEventReader(controller)
	history.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("session-1")).Return(messageHistory([]alfred.RunEvent{
		{Payload: alfred.MessageAdded{Message: message}},
		{Payload: alfred.ToolCallRequested{ModelCallID: "model-call-1", Call: first}},
		{Payload: alfred.ToolCallRequested{ModelCallID: "model-call-1", Call: second}},
		{Payload: alfred.ModelCallCompleted{CallID: "model-call-1"}},
		{Payload: alfred.ToolCallCompleted{Result: secondResult}},
		{Payload: alfred.ToolCallCompleted{Result: firstResult}},
	}))
	built, err := New(Config{}, catalog, history, testProjects(t)).Build(t.Context(), alfred.ContextRequest{SessionID: "session-1", RunID: "current"})
	want := []alfred.ModelInput{{Payload: message}, {Payload: first}, {Payload: second}, {Payload: secondResult}, {Payload: firstResult}}
	if err != nil || !reflect.DeepEqual(built.Input, want) {
		t.Fatalf("history input = %#v, error = %v, want %#v", built.Input, err, want)
	}
}
