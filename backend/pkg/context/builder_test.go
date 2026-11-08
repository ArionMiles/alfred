package context

import (
	stdcontext "context"
	"iter"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestBuilderBuildsInitialRunContext(t *testing.T) {
	t.Parallel()

	tool := alfred.ToolDefinition{ID: "read", Name: "Read"}
	catalog := mocks.NewMockToolCatalog(gomock.NewController(t))
	catalog.EXPECT().ListTools(gomock.Any()).Return([]alfred.ToolDefinition{tool}, nil)
	history := mocks.NewMockRunEventReader(gomock.NewController(t))
	history.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("session-1")).Return(messageHistory(nil))
	builder := New(Config{}, catalog, history, testProjects(t))
	request := alfred.ContextRequest{
		RunID:     "run-1",
		SessionID: "session-1",
		Invocation: alfred.Invocation{
			Input: []alfred.ContentPart{alfred.TextContent{Text: "Review this change"}},
		},
		SkillCatalog: alfred.SkillCatalog{
			Skills: []alfred.SkillDefinition{
				{
					Name:        "code-review",
					Description: "Review <code> carefully.",
					Location:    "/skills/code-review/SKILL.md",
				},
			},
		},
	}

	modelContext, err := builder.Build(stdcontext.Background(), request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(modelContext.Input) != 1 {
		t.Fatalf("len(Input) = %d, want 1", len(modelContext.Input))
	}
	message, ok := modelContext.Input[0].Payload.(alfred.Message)
	if !ok {
		t.Fatalf("Input[0] = %T, want alfred.Message", modelContext.Input[0])
	}
	if message.Role != alfred.MessageRoleUser {
		t.Fatalf("message Role = %q, want %q", message.Role, alfred.MessageRoleUser)
	}
	if len(message.Content) != 1 {
		t.Fatalf("len(message Content) = %d, want 1", len(message.Content))
	}
	if len(modelContext.Tools) != 1 || modelContext.Tools[0].ID != tool.ID {
		t.Fatalf("Tools = %#v, want %#v", modelContext.Tools, []alfred.ToolDefinition{tool})
	}
	if !strings.Contains(modelContext.Instructions, "<name>code-review</name>") {
		t.Fatalf("Instructions = %q, want skill name", modelContext.Instructions)
	}
	if !strings.Contains(modelContext.Instructions, "Review &lt;code&gt; carefully.") {
		t.Fatalf("Instructions = %q, want escaped description", modelContext.Instructions)
	}
}

func TestBuilderIncludesEarlierConversationAndKeepsResumeBoundary(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	catalog := mocks.NewMockToolCatalog(controller)
	catalog.EXPECT().ListTools(gomock.Any()).Return(nil, nil).Times(2)
	history := mocks.NewMockRunEventReader(controller)
	user := alfred.Message{Role: alfred.MessageRoleUser, Content: []alfred.ContentPart{alfred.TextContent{Text: "My name is Kanishk"}}}
	assistant := alfred.Message{Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Hello, Kanishk"}}}
	current := alfred.Message{Role: alfred.MessageRoleUser, Content: []alfred.ContentPart{alfred.TextContent{Text: "What is my name?"}}}
	events := []alfred.RunEvent{
		{RunID: "earlier", Payload: alfred.MessageAdded{Message: user}},
		{RunID: "earlier", Payload: alfred.ToolCallRequested{}},
		{RunID: "earlier", Payload: alfred.MessageAdded{Message: assistant}},
		{RunID: "current", Payload: alfred.MessageAdded{Message: current}},
	}
	gomock.InOrder(
		history.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("session-1")).Return(messageHistory(events)),
		history.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("session-1")).Return(messageHistory(append(events,
			alfred.RunEvent{RunID: "current", Payload: alfred.MessageAdded{Message: assistant}},
			alfred.RunEvent{RunID: "later", Payload: alfred.MessageAdded{Message: user}},
		))),
	)
	builder := New(Config{}, catalog, history, testProjects(t))
	request := alfred.ContextRequest{SessionID: "session-1", RunID: "current", Invocation: alfred.Invocation{Input: current.Content}}
	for range 2 {
		built, err := builder.Build(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(built.Input, []alfred.ModelInput{{Payload: user}, {Payload: assistant}, {Payload: current}}) {
			t.Fatalf("model input = %#v, want earlier conversation and one current invocation", built.Input)
		}
	}
}

func TestBuilderReadsOnlyRequestedSession(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	catalog := mocks.NewMockToolCatalog(controller)
	catalog.EXPECT().ListTools(gomock.Any()).Return(nil, nil).Times(2)
	history := mocks.NewMockRunEventReader(controller)
	message := alfred.Message{Role: alfred.MessageRoleUser, Content: []alfred.ContentPart{alfred.TextContent{Text: "Session one only"}}}
	history.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("session-1")).Return(messageHistory([]alfred.RunEvent{
		{RunID: "earlier", Payload: alfred.MessageAdded{Message: message}},
		{RunID: "current", Payload: alfred.MessageAdded{}},
	}))
	history.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("session-2")).Return(messageHistory(nil))
	builder := New(Config{}, catalog, history, testProjects(t))
	first, err := builder.Build(t.Context(), alfred.ContextRequest{SessionID: "session-1", RunID: "current"})
	if err != nil || len(first.Input) != 1 {
		t.Fatalf("first session input = %#v, error = %v", first.Input, err)
	}
	second, err := builder.Build(t.Context(), alfred.ContextRequest{SessionID: "session-2", RunID: "other"})
	if err != nil || len(second.Input) != 0 {
		t.Fatalf("second session input = %#v, error = %v", second.Input, err)
	}
}

func TestBuilderPropagatesHistoryReadFailure(t *testing.T) {
	const op = "TestBuilderPropagatesHistoryReadFailure"

	t.Parallel()
	controller := gomock.NewController(t)
	catalog := mocks.NewMockToolCatalog(controller)
	catalog.EXPECT().ListTools(gomock.Any()).Return(nil, nil)
	history := mocks.NewMockRunEventReader(controller)
	readErr := errors.B.Op(op).KindUnavailable().Text("history cannot be read").Build()
	history.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("session-1")).Return(func(yield func(alfred.RunEvent, error) bool) {
		yield(alfred.RunEvent{}, readErr)
	})
	built, err := New(Config{}, catalog, history, testProjects(t)).Build(t.Context(), alfred.ContextRequest{SessionID: "session-1", RunID: "current"})
	if !errors.Is(err, readErr) || len(built.Input) != 0 {
		t.Fatalf("Build() = %#v, %v, want history failure", built, err)
	}
}

func messageHistory(events []alfred.RunEvent) iter.Seq2[alfred.RunEvent, error] {
	return func(yield func(alfred.RunEvent, error) bool) {
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func TestBuilderIncludesCommittedReasoningInFollowupContext(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	catalog := mocks.NewMockToolCatalog(controller)
	catalog.EXPECT().ListTools(gomock.Any()).Return(nil, nil)
	reasoning := alfred.Reasoning{
		Text: "Earlier reasoning summary", TextKind: alfred.ReasoningTextSummary,
	}
	message := alfred.Message{Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Earlier answer"}}}
	current := alfred.Message{Role: alfred.MessageRoleUser, Content: []alfred.ContentPart{alfred.TextContent{Text: "Follow up"}}}
	history := mocks.NewMockRunEventReader(controller)
	history.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("session-1")).Return(messageHistory([]alfred.RunEvent{
		{RunID: "earlier", Payload: alfred.ModelCallCompleted{CallID: "call-1", Output: []alfred.ModelInput{{Payload: reasoning, Native: alfred.NativeData{"test.reasoning.v1": []byte(`{"state":"opaque"}`)}}, {Payload: message}}}},
		{RunID: "earlier", Payload: alfred.MessageAdded{ModelCallID: "call-1", Message: message}},
		{RunID: "current", Payload: alfred.MessageAdded{Message: current}},
	}))
	built, err := New(Config{}, catalog, history, testProjects(t)).Build(t.Context(), alfred.ContextRequest{
		SessionID: "session-1", RunID: "current", Invocation: alfred.Invocation{Input: current.Content},
	})
	want := []alfred.ModelInput{{Payload: reasoning, Native: alfred.NativeData{"test.reasoning.v1": []byte(`{"state":"opaque"}`)}}, {Payload: message}, {Payload: current}}
	if err != nil || !reflect.DeepEqual(built.Input, want) {
		t.Fatalf("follow-up context = %#v, error = %v, want %#v", built.Input, err, want)
	}
}

func testProjects(t *testing.T) *mocks.MockProjectStore {
	t.Helper()
	projects := mocks.NewMockProjectStore(gomock.NewController(t))
	projects.EXPECT().GetProject(gomock.Any(), gomock.Any()).Return(alfred.Project{Root: t.TempDir()}, nil).AnyTimes()
	return projects
}
