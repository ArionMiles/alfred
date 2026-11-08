package agent

import (
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	contextbuilder "github.com/ArionMiles/alfred/pkg/context"
)

func TestRunnerSendsPriorConversationWithConcreteContextBuilder(t *testing.T) {
	t.Parallel()
	for _, status := range []alfred.RunStatus{alfred.RunStatusQueued, alfred.RunStatusInterrupted} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			model := testModel()
			run := testRun(model.Ref)
			run.Status = status
			fixture := newRunnerFixture(t, run, model)
			priorUser := userMessage("My name is Kanishk")
			priorAssistant := alfred.Message{
				Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Hello, Kanishk"}},
			}
			current := userMessage("What is my name?")
			priorCall := alfred.ToolCall{ID: "prior-call", RunID: "earlier", ToolID: "read"}
			priorResult := alfred.ToolResult{CallID: priorCall.ID, Output: priorAssistant.Content}
			fixture.events.events = []alfred.RunEvent{{RunID: run.ID, Payload: alfred.MessageAdded{Message: current}}}
			want := []alfred.ModelInput{{Payload: priorUser}, {Payload: priorAssistant}, {Payload: priorCall}, {Payload: priorResult}, {Payload: current}}
			// Historical calls belong in model context but must never be executed.
			fixture.dependencies.Executor = mocks.NewMockExecutor(fixture.controller)
			if status == alfred.RunStatusInterrupted {
				partial := alfred.Message{
					Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Let me check"}},
				}
				fixture.events.events = append(fixture.events.events, alfred.RunEvent{RunID: run.ID, Payload: alfred.MessageAdded{Message: partial}})
				want = append(want, alfred.ModelInput{Payload: partial})
			}
			events := append([]alfred.RunEvent{
				{RunID: "earlier", Payload: alfred.MessageAdded{Message: priorUser}},
				{RunID: "earlier", Payload: alfred.MessageAdded{Message: priorAssistant}},
				{RunID: "earlier", Payload: alfred.ToolCallRequested{ModelCallID: "prior-model-call", Call: priorCall}},
				{RunID: "earlier", Payload: alfred.ModelCallCompleted{CallID: "prior-model-call"}},
				{RunID: "earlier", Payload: alfred.ToolCallCompleted{Result: priorResult}},
			}, fixture.events.snapshot()...)
			history := mocks.NewMockRunEventReader(fixture.controller)
			history.EXPECT().ReadSessionEvents(gomock.Any(), run.SessionID).Return(func(yield func(alfred.RunEvent, error) bool) {
				for _, event := range events {
					if !yield(event, nil) {
						return
					}
				}
			})
			tools := mocks.NewMockToolCatalog(fixture.controller)
			tools.EXPECT().ListTools(gomock.Any()).Return(nil, nil)
			projects := mocks.NewMockProjectStore(fixture.controller)
			projects.EXPECT().GetProject(gomock.Any(), gomock.Any()).Return(alfred.Project{Root: t.TempDir()}, nil)
			fixture.dependencies.Contexts = contextbuilder.New(contextbuilder.Config{}, tools, history, projects)
			invocations := mocks.NewMockInvocationStore(fixture.controller)
			invocations.EXPECT().GetInvocation(gomock.Any(), run.InvocationID).Return(alfred.Invocation{
				ID: run.InvocationID, SessionID: run.SessionID, Input: current.Content,
			}, nil)
			fixture.dependencies.Invocations = invocations
			if err := fixture.runner().Execute(t.Context(), run.ID); err != nil {
				t.Fatal(err)
			}
			requests := fixture.provider.snapshotRequests()
			if len(requests) != 1 || !reflect.DeepEqual(requests[0].Input, want) {
				t.Fatalf("model requests = %#v, want conversation input %#v", requests, want)
			}
		})
	}
}
