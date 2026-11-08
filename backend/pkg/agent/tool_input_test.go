package agent

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
)

func TestToolInputRequestWaitsAndResumesWithoutRepeatingTool(t *testing.T) {
	t.Parallel()
	model := testModel()
	run := testRun(model.Ref)
	fixture := newRunnerFixture(t, run, model)
	call := alfred.ToolCall{ID: "ask-call", RunID: run.ID, ToolID: "ask_user"}
	fixture.provider.streams = [][]alfred.ModelEvent{
		{{Payload: alfred.ModelToolCall{Call: call}}, {Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonToolCall}}},
		{{Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonStop}}},
	}
	executor := mocks.NewMockExecutor(fixture.controller)
	executor.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(alfred.ExecutionOutcome{
		Decision: alfred.PolicyDecision{Outcome: alfred.PolicyOutcomeAllow}, InputPrompt: []alfred.ContentPart{alfred.TextContent{Text: "Which branch?"}},
	}, nil).Times(1)
	fixture.dependencies.Executor = executor
	runner := fixture.runner()
	if err := runner.Execute(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if fixture.runs.status() != alfred.RunStatusWaitingForInput {
		t.Fatalf("status=%v", fixture.runs.status())
	}
	var request alfred.InputRequest
	for _, event := range fixture.events.snapshot() {
		if input, ok := event.Payload.(alfred.InputRequested); ok {
			request = input.Request
		}
	}
	if request.CallID != call.ID || len(request.Prompt) != 1 {
		t.Fatalf("request=%v", request)
	}
	dispatcher := NewDispatcher(t.Context(), runner)
	if err := dispatcher.ProvideInput(t.Context(), run.ID, alfred.InputResponse{RequestID: request.ID, Content: []alfred.ContentPart{alfred.TextContent{Text: "main"}}}); err != nil {
		t.Fatal(err)
	}
	waitForInactive(t, dispatcher, run.ID)
	requests := fixture.provider.snapshotRequests()
	if len(requests) != 2 {
		t.Fatalf("requests=%v", requests)
	}
	last := requests[1].Input[len(requests[1].Input)-1].Payload
	result, ok := last.(alfred.ToolResult)
	if !ok || result.CallID != call.ID || result.Output[0] != (alfred.TextContent{Text: "main"}) {
		t.Fatalf("response=%v", last)
	}
}
