package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
)

func TestRunnerPreservesCompletedOutputOrderInLiveContextAndReplay(t *testing.T) {
	t.Parallel()
	model := testModel()
	run := testRun(model.Ref)
	fixture := newRunnerFixture(t, run, model)
	base := []alfred.ModelInput{{Payload: userMessage("Check the file")}}
	fixture.modelContext.Input = base
	before := alfred.Message{Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Before the call"}}}
	after := alfred.Message{Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "After the call"}}}
	call := alfred.ToolCall{ID: "call-1", RunID: run.ID, ToolID: "read"}
	result := alfred.ToolResult{CallID: call.ID, Output: []alfred.ContentPart{alfred.TextContent{Text: "file contents"}}}
	reasoning := alfred.Reasoning{Text: "Inspect the file first", TextKind: alfred.ReasoningTextSummary}
	opaque := alfred.Reasoning{}
	reasoningNative := alfred.NativeData{"test.reasoning.v1": json.RawMessage(`{"state":"opaque-before"}`)}
	opaqueNative := alfred.NativeData{"test.reasoning.v1": json.RawMessage(`{"state":"opaque-after"}`)}
	messageNative := alfred.NativeData{"test.text.v1": json.RawMessage(`{"phase":"commentary"}`)}
	callNative := alfred.NativeData{"test.call.v1": json.RawMessage(`{"signature":"call-proof"}`)}

	fixture.provider.streams = [][]alfred.ModelEvent{
		{
			{Payload: alfred.ModelReasoning{Reasoning: reasoning}, Native: reasoningNative},
			{Payload: alfred.ModelContentDelta{Delta: alfred.TextContent{Text: "Before"}}},
			{Payload: alfred.ModelMessage{Message: before}, Native: messageNative},
			{Payload: alfred.ModelToolCall{Call: call}, Native: callNative},
			{Payload: alfred.ModelReasoning{Reasoning: opaque}, Native: opaqueNative},
			{Payload: alfred.ModelMessage{Message: after}},
			{Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonToolCall}},
		},
		{{Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonStop}}},
	}
	executor := mocks.NewMockExecutor(fixture.controller)
	executor.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(alfred.ExecutionOutcome{
		Decision: alfred.PolicyDecision{Outcome: alfred.PolicyOutcomeAllow}, Result: &result,
	}, nil)
	fixture.dependencies.Executor = executor
	runner := fixture.runner()
	if err := runner.Execute(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	requests := fixture.provider.snapshotRequests()
	want := []alfred.ModelInput{base[0], {Payload: reasoning, Native: reasoningNative}, {Payload: before, Native: messageNative}, {Payload: call, Native: callNative}, {Payload: opaque, Native: opaqueNative}, {Payload: after}, {Payload: result}}
	if len(requests) != 2 || !reflect.DeepEqual(requests[1].Input, want) {
		t.Fatalf("live requests = %#v, want ordered input %#v", requests, want)
	}
	restored, err := runner.restoreContinuation(t.Context(), run.ID, base)
	if err != nil || !reflect.DeepEqual(restored.input, want) || len(restored.pendingToolCalls) != 0 {
		t.Fatalf("replayed continuation = %#v, error = %v", restored, err)
	}
	var transcript []alfred.Message
	for _, event := range fixture.events.snapshot() {
		if added, ok := event.Payload.(alfred.MessageAdded); ok {
			if added.ModelCallID != requests[0].ID {
				t.Fatal("transcript message was not associated with its committed output")
			}
			transcript = append(transcript, added.Message)
		}
	}
	if !reflect.DeepEqual(transcript, []alfred.Message{before, after}) {
		t.Fatalf("transcript = %#v", transcript)
	}
}

func TestRunnerDoesNotCommitOutputsFromIncompleteStream(t *testing.T) {
	t.Parallel()
	model := testModel()
	run := testRun(model.Ref)
	fixture := newRunnerFixture(t, run, model)
	fixture.dependencies.Executor = mocks.NewMockExecutor(fixture.controller)
	fixture.provider.events = []alfred.ModelEvent{
		{Payload: alfred.ModelReasoning{Reasoning: alfred.Reasoning{Text: "Uncommitted reasoning", TextKind: alfred.ReasoningTextReasoning}}},
		{Payload: alfred.ModelMessage{Message: alfred.Message{
			Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Uncommitted"}},
		}}},
		{Payload: alfred.ModelToolCall{Call: alfred.ToolCall{ID: "uncommitted-call", ToolID: "read"}}},
	}
	runner := fixture.runner()
	if err := runner.Execute(t.Context(), run.ID); err == nil {
		t.Fatal("incomplete model stream was accepted")
	}
	for _, event := range fixture.events.snapshot() {
		switch event.Payload.(type) {
		case alfred.ModelCallCompleted, alfred.MessageAdded:
			t.Fatal("incomplete model stream committed output or transcript messages")
		}
	}
	restored, err := runner.restoreContinuation(t.Context(), run.ID, nil)
	if err != nil || len(restored.input) != 0 || len(restored.pendingToolCalls) != 0 {
		t.Fatalf("incomplete output survived replay: %#v, %v", restored, err)
	}
}

func TestRunnerRejectsDeltasWithoutCompleteMessage(t *testing.T) {
	t.Parallel()
	model := testModel()
	run := testRun(model.Ref)
	fixture := newRunnerFixture(t, run, model)
	fixture.provider.events = []alfred.ModelEvent{
		{Payload: alfred.ModelContentDelta{Delta: alfred.TextContent{Text: "Missing complete message"}}},
		{Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonStop}},
	}
	if err := fixture.runner().Execute(t.Context(), run.ID); err == nil {
		t.Fatal("streamed text was silently discarded without a completed message")
	}
	for _, event := range fixture.events.snapshot() {
		if _, committed := event.Payload.(alfred.ModelCallCompleted); committed {
			t.Fatal("invalid stream committed an empty output")
		}
	}
}
