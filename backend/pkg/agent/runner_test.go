package agent

import (
	"context"
	"iter"
	"net/http"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestRunnerDiscoversSkillsOnceWhenPreparingRun(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	fixture := newRunnerFixture(t, run, model)
	fixture.skillCatalog = alfred.SkillCatalog{
		Skills: []alfred.SkillDefinition{{Name: "code-review"}},
	}

	if err := fixture.runner().Execute(t.Context(), run.ID); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(fixture.skillProjects) != 1 || fixture.skillProjects[0] != "project-1" {
		t.Fatalf("skill projects = %#v, want project-1", fixture.skillProjects)
	}
	if len(fixture.contextRequests) != 1 {
		t.Fatalf("Build() calls = %d, want 1", len(fixture.contextRequests))
	}
	request := fixture.contextRequests[0]
	if request.RunID != run.ID || request.Invocation.ID != run.InvocationID {
		t.Fatalf("Build() request = %#v", request)
	}
	if len(request.SkillCatalog.Skills) != 1 || request.SkillCatalog.Skills[0].Name != "code-review" {
		t.Fatalf("Build() skill catalog = %#v", request.SkillCatalog)
	}
	requests := fixture.provider.snapshotRequests()
	if len(requests) != 1 || requests[0].ID == "" {
		t.Fatalf("model requests = %#v, want one identified call", requests)
	}
}

func TestRunnerReturnsNotFoundWhenRunModelIsUnavailable(t *testing.T) {
	t.Parallel()

	run := testRun(alfred.ModelRef{ProviderID: "provider-1", ModelID: "missing"})
	fixture := newRunnerFixture(t, run, alfred.ModelInfo{})

	err := fixture.runner().Execute(t.Context(), run.ID)
	if kind := errors.WhatKind(err); kind != errors.NotFound {
		t.Fatalf("errors.WhatKind(error) = %#v, want %#v", kind, errors.NotFound)
	}
	if fixture.runs.status() != alfred.RunStatusFailed {
		t.Fatalf("run status = %q, want %q", fixture.runs.status(), alfred.RunStatusFailed)
	}
}

func TestRunnerStreamsAndRecordsModelResponse(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	fixture := newRunnerFixture(t, run, model)
	fixture.provider.events = []alfred.ModelEvent{
		{CallID: "model-call-1", Payload: alfred.ModelContentDelta{Index: 0, Delta: alfred.TextContent{Text: "Hello"}}},
		{CallID: "model-call-1", Payload: alfred.ModelContentDelta{Index: 0, Delta: alfred.TextContent{Text: " there"}}},
		{CallID: "model-call-1", Payload: alfred.ModelUsage{Tokens: alfred.TokenUsage{InputTokens: 12, OutputTokens: 2}}},
		{CallID: "model-call-1", Payload: alfred.ModelMessage{Message: alfred.Message{
			Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Hello there"}},
		}}},
		{CallID: "model-call-1", Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonStop}},
	}

	if err := fixture.runner().Execute(t.Context(), run.ID); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fixture.runs.status() != alfred.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", fixture.runs.status())
	}
	updates := fixture.updates.snapshot()
	if len(updates) != 2 {
		t.Fatalf("published updates = %d, want 2", len(updates))
	}
	for _, update := range updates {
		if update.SessionID != run.SessionID {
			t.Fatalf("update session ID = %q, want %q", update.SessionID, run.SessionID)
		}
	}
	var message alfred.Message
	var completed bool
	for _, event := range fixture.events.snapshot() {
		switch payload := event.Payload.(type) {
		case alfred.MessageAdded:
			message = payload.Message
		case alfred.ModelCallCompleted:
			requests := fixture.provider.snapshotRequests()
			completed = len(requests) == 1 && requests[0].ID != "" && payload.CallID == requests[0].ID
		}
	}
	if len(message.Content) != 1 || message.Content[0].(alfred.TextContent).Text != "Hello there" {
		t.Fatalf("assistant content = %#v", message.Content)
	}
	if !completed {
		t.Fatal("completed model call was not committed")
	}
}

func TestRunnerInterruptsOnRetryableProviderError(t *testing.T) {
	t.Parallel()

	usageLimitReached := errors.Kind{Code: "usage_limit_reached", Status: http.StatusTooManyRequests}
	model := testModel()
	run := testRun(model.Ref)
	fixture := newRunnerFixture(t, run, model)
	fixture.provider.streamErr = errors.B.
		Op("Stream").
		Kind(usageLimitReached).
		UserMsg("usage limit exhausted; resume after the limit resets").
		Retryable(true).
		Build()

	if err := fixture.runner().Execute(t.Context(), run.ID); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fixture.runs.status() != alfred.RunStatusInterrupted {
		t.Fatalf("run status = %q, want interrupted", fixture.runs.status())
	}
	var interruption alfred.RunInterrupted
	for _, event := range fixture.events.snapshot() {
		if payload, ok := event.Payload.(alfred.RunInterrupted); ok {
			interruption = payload
		}
	}
	if interruption.Reason != usageLimitReached.Code ||
		interruption.Message != "usage limit exhausted; resume after the limit resets" {
		t.Fatalf("interruption = %#v", interruption)
	}
}

func TestRunnerRestoresCompletedToolExchangeBeforeRetryingModel(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	run.Status = alfred.RunStatusInterrupted
	call := alfred.ToolCall{ID: "tool-call-1", RunID: run.ID, ToolID: "read"}
	result := alfred.ToolResult{
		CallID: call.ID,
		Output: []alfred.ContentPart{alfred.TextContent{Text: "contents"}},
	}
	fixture := newRunnerFixture(t, run, model)
	fixture.modelContext.Input = []alfred.ModelInput{{Payload: userMessage("Start")}}
	fixture.events.events = []alfred.RunEvent{
		{Payload: alfred.ToolCallRequested{ModelCallID: "model-call-1", Call: call}},
		{Payload: alfred.ModelCallCompleted{CallID: "model-call-1"}},
		{Payload: alfred.ToolCallCompleted{Result: result}},
	}

	if err := fixture.runner().Execute(t.Context(), run.ID); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	requests := fixture.provider.snapshotRequests()
	if len(requests) != 1 || len(requests[0].Input) != 3 {
		t.Fatalf("model requests = %#v", requests)
	}
	if requests[0].Input[1].Payload.(alfred.ToolCall).ID != call.ID ||
		requests[0].Input[2].Payload.(alfred.ToolResult).CallID != call.ID {
		t.Fatalf("restored model input = %#v", requests[0].Input)
	}
}

func TestRunnerExecutesPendingCallFromCompletedModelCall(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	run.Status = alfred.RunStatusRunning
	call := alfred.ToolCall{ID: "tool-call-1", RunID: run.ID, ToolID: "read"}
	fixture := newRunnerFixture(t, run, model)
	fixture.events.events = []alfred.RunEvent{
		{Payload: alfred.ToolCallRequested{ModelCallID: "model-call-1", Call: call}},
		{Payload: alfred.ModelCallCompleted{CallID: "model-call-1"}},
	}
	executor := mocks.NewMockExecutor(fixture.controller)
	executor.EXPECT().Execute(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		request alfred.ExecutionRequest,
	) (alfred.ExecutionOutcome, error) {
		return alfred.ExecutionOutcome{
			Decision: alfred.PolicyDecision{Outcome: alfred.PolicyOutcomeAllow},
			Result:   &alfred.ToolResult{CallID: request.Call.ID},
		}, nil
	})
	fixture.dependencies.Executor = executor

	if err := fixture.runner().Execute(t.Context(), run.ID); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := fixture.provider.snapshotRequests()[0].Input; len(got) != 2 {
		t.Fatalf("model input = %#v", got)
	}
}

func TestRunnerResumeRunIgnoresToolCallsFromFailedModelCall(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	run.Status = alfred.RunStatusInterrupted
	fixture := newRunnerFixture(t, run, model)
	fixture.modelContext.Input = []alfred.ModelInput{{Payload: userMessage("Start")}}
	fixture.events.events = []alfred.RunEvent{
		{Payload: alfred.ToolCallRequested{
			ModelCallID: "model-call-1",
			Call:        alfred.ToolCall{ID: "partial-tool-call"},
		}},
		{Payload: alfred.ModelCallRecorded{Metrics: alfred.ModelCallMetrics{
			CallID: "model-call-1", Outcome: alfred.ModelCallOutcomeFailed,
		}}},
	}
	dispatcher := newTestDispatcher(t, fixture.runner())

	if err := dispatcher.ResumeRun(t.Context(), run.ID); err != nil {
		t.Fatalf("ResumeRun() error = %v", err)
	}
	waitForInactive(t, dispatcher, run.ID)
	if got := fixture.provider.snapshotRequests()[0].Input; len(got) != 1 {
		t.Fatalf("model input = %#v", got)
	}
	if fixture.runs.status() != alfred.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", fixture.runs.status())
	}
}

func TestRunnerResumeRunRejectsRunThatIsNotInterrupted(t *testing.T) {
	t.Parallel()

	run := testRun(testModel().Ref)
	run.Status = alfred.RunStatusCompleted
	fixture := newRunnerFixture(t, run, testModel())
	err := newTestDispatcher(t, fixture.runner()).ResumeRun(t.Context(), run.ID)
	if kind := errors.WhatKind(err); kind != errors.Conflict {
		t.Fatalf("errors.WhatKind(error) = %#v, want %#v", kind, errors.Conflict)
	}
}

func TestDispatcherOwnsActiveRunLifetimeAndCancellation(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	run.Status = alfred.RunStatusRunning
	fixture := newRunnerFixture(t, run, model)
	fixture.provider.started = make(chan struct{})
	fixture.provider.contexts = make(chan context.Context, 1)
	fixture.provider.waitForCancellation = true
	cancellationEvent := make(chan struct{})
	releaseCancellationEvent := make(chan struct{})
	fixture.events.beforeAppend = func(_ context.Context, event alfred.NewRunEvent) {
		state, ok := event.Payload.(alfred.RunStateChanged)
		if !ok || state.Status != alfred.RunStatusCancelled {
			return
		}
		close(cancellationEvent)
		<-releaseCancellationEvent
	}
	dispatcher := newTestDispatcher(t, fixture.runner())
	requestContext, cancelRequest := context.WithCancel(t.Context())
	if err := dispatcher.Dispatch(requestContext, run.ID); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	<-fixture.provider.started
	runContext := <-fixture.provider.contexts
	cancelRequest()
	if err := runContext.Err(); err != nil {
		t.Fatalf("run context after request cancellation = %v", err)
	}
	if err := dispatcher.Dispatch(t.Context(), run.ID); errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("second Dispatch() error = %v, want conflict", err)
	}
	cancelled := make(chan error, 1)
	go func() {
		cancelled <- dispatcher.CancelRun(context.Background(), run.ID)
	}()
	<-cancellationEvent
	if err := runContext.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("run context before cancellation event = %v", err)
	}
	close(releaseCancellationEvent)
	if err := <-cancelled; err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	waitForInactive(t, dispatcher, run.ID)
	if fixture.runs.status() != alfred.RunStatusCancelled {
		t.Fatalf("run status = %q, want cancelled", fixture.runs.status())
	}
}

func TestRunnerContinuesAfterAllowedToolCall(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	call := alfred.ToolCall{ID: "tool-call-1", ToolID: "read"}
	fixture := newRunnerFixture(t, run, model)
	fixture.provider.streams = [][]alfred.ModelEvent{
		{{Payload: alfred.ModelToolCall{Call: call}}, {Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonToolCall}}},
		{{Payload: alfred.ModelMessage{Message: alfred.Message{
			Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Done"}},
		}}}, {Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonStop}}},
	}

	executor := mocks.NewMockExecutor(fixture.controller)
	executor.EXPECT().Execute(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		request alfred.ExecutionRequest,
	) (alfred.ExecutionOutcome, error) {
		return alfred.ExecutionOutcome{
			Decision: alfred.PolicyDecision{Outcome: alfred.PolicyOutcomeAllow},
			Result: &alfred.ToolResult{
				CallID: request.Call.ID,
				Output: []alfred.ContentPart{alfred.TextContent{Text: "contents"}},
			},
		}, nil
	})
	fixture.dependencies.Executor = executor

	if err := fixture.runner().Execute(t.Context(), run.ID); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	requests := fixture.provider.snapshotRequests()
	if fixture.runs.status() != alfred.RunStatusCompleted || len(requests) != 2 {
		t.Fatalf("status = %q, requests = %d", fixture.runs.status(), len(requests))
	}
	if requests[0].ID == "" || requests[1].ID == "" || requests[0].ID == requests[1].ID {
		t.Fatalf("model calls must have distinct nonempty IDs: %q, %q", requests[0].ID, requests[1].ID)
	}
	secondInput := requests[1].Input
	if len(secondInput) != 2 || secondInput[0].Payload.(alfred.ToolCall).RunID != run.ID ||
		secondInput[1].Payload.(alfred.ToolResult).CallID != call.ID {
		t.Fatalf("second model input = %#v", secondInput)
	}
	var policyRecorded, resultRecorded bool
	for _, event := range fixture.events.snapshot() {
		switch event.Payload.(type) {
		case alfred.PolicyEvaluated:
			policyRecorded = true
		case alfred.ToolCallCompleted:
			resultRecorded = true
		}
	}
	if !policyRecorded || !resultRecorded {
		t.Fatalf("policy recorded = %t, result recorded = %t", policyRecorded, resultRecorded)
	}
}

func TestRunnerWaitsWhenToolCallRequiresApproval(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	call := alfred.ToolCall{ID: "tool-call-1", ToolID: "shell"}
	fixture := newRunnerFixture(t, run, model)
	fixture.provider.events = []alfred.ModelEvent{
		{Payload: alfred.ModelToolCall{Call: call}},
		{Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonToolCall}},
	}

	executor := mocks.NewMockExecutor(fixture.controller)
	executor.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(alfred.ExecutionOutcome{
		Decision:     alfred.PolicyDecision{Outcome: alfred.PolicyOutcomeAsk, Reason: "shell requires approval"},
		ActionDigest: "action-digest",
	}, nil)
	fixture.dependencies.Executor = executor

	if err := fixture.runner().Execute(t.Context(), run.ID); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fixture.runs.status() != alfred.RunStatusWaitingForApproval {
		t.Fatalf("run status = %q", fixture.runs.status())
	}
	var request alfred.ApprovalRequest
	for _, event := range fixture.events.snapshot() {
		if payload, ok := event.Payload.(alfred.ApprovalRequested); ok {
			request = payload.Request
		}
	}
	if request.ID == "" || request.CallID != call.ID || request.ActionDigest != "action-digest" {
		t.Fatalf("approval request = %#v", request)
	}
}

func TestRunnerResumesApprovedToolCallWithGrant(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	run.Status = alfred.RunStatusWaitingForApproval
	call := alfred.ToolCall{ID: "tool-call-1", RunID: run.ID, ToolID: "shell"}
	request := alfred.ApprovalRequest{ID: "approval-1", CallID: call.ID, ActionDigest: "action-digest"}
	fixture := newRunnerFixture(t, run, model)
	fixture.events.events = pendingApprovalEvents(run.ID, call, request)
	executor := mocks.NewMockExecutor(fixture.controller)
	executor.EXPECT().Execute(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		execution alfred.ExecutionRequest,
	) (alfred.ExecutionOutcome, error) {
		if execution.Approval == nil || execution.Approval.ApprovalID != request.ID ||
			execution.Approval.ActionDigest != request.ActionDigest {
			t.Fatalf("approval grant = %#v", execution.Approval)
		}
		return alfred.ExecutionOutcome{
			Decision: alfred.PolicyDecision{Outcome: alfred.PolicyOutcomeAllow},
			Result:   &alfred.ToolResult{CallID: execution.Call.ID},
		}, nil
	})
	fixture.dependencies.Executor = executor
	dispatcher := newTestDispatcher(t, fixture.runner())
	previousExecution := &activeRun{done: make(chan struct{})}
	dispatcher.active[run.ID] = previousExecution
	resolved := make(chan error, 1)
	go func() {
		resolved <- dispatcher.ResolveApproval(t.Context(), run.ID, alfred.ApprovalResolution{
			ApprovalID: request.ID, Outcome: alfred.ApprovalOutcomeApproved,
		})
	}()
	waitForRunEvent[alfred.ApprovalResolved](t, fixture.events)
	select {
	case err := <-resolved:
		t.Fatalf("ResolveApproval() returned before prior execution finished: %v", err)
	default:
	}
	dispatcher.activeMu.Lock()
	delete(dispatcher.active, run.ID)
	close(previousExecution.done)
	dispatcher.activeMu.Unlock()
	if err := <-resolved; err != nil {
		t.Fatalf("ResolveApproval() error = %v", err)
	}
	waitForInactive(t, dispatcher, run.ID)
	requests := fixture.provider.snapshotRequests()
	if len(requests) != 1 || len(requests[0].Input) != 2 {
		t.Fatalf("model requests = %#v", requests)
	}
}

func TestRunnerResumesAfterDeniedApprovalWithoutExecutingTool(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	run.Status = alfred.RunStatusWaitingForApproval
	call := alfred.ToolCall{ID: "tool-call-1", RunID: run.ID, ToolID: "shell"}
	request := alfred.ApprovalRequest{ID: "approval-1", CallID: call.ID}
	fixture := newRunnerFixture(t, run, model)
	fixture.events.events = pendingApprovalEvents(run.ID, call, request)
	fixture.dependencies.Executor = mocks.NewMockExecutor(fixture.controller)
	dispatcher := newTestDispatcher(t, fixture.runner())

	if err := dispatcher.ResolveApproval(t.Context(), run.ID, alfred.ApprovalResolution{
		ApprovalID: request.ID, Outcome: alfred.ApprovalOutcomeDenied,
	}); err != nil {
		t.Fatalf("ResolveApproval() error = %v", err)
	}
	waitForInactive(t, dispatcher, run.ID)
	input := fixture.provider.snapshotRequests()[0].Input
	result := input[1].Payload.(alfred.ToolResult)
	if len(input) != 2 || !result.IsError || result.CallID != call.ID {
		t.Fatalf("model input = %#v", input)
	}
}

func TestRunnerResumesWithRequestedUserInput(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	run.Status = alfred.RunStatusWaitingForInput
	request := alfred.InputRequest{ID: "input-1"}
	fixture := newRunnerFixture(t, run, model)
	fixture.events.events = []alfred.RunEvent{{RunID: run.ID, Payload: alfred.InputRequested{Request: request}}}
	response := alfred.InputResponse{
		RequestID: request.ID,
		Content:   []alfred.ContentPart{alfred.TextContent{Text: "Use Friday"}},
	}
	dispatcher := newTestDispatcher(t, fixture.runner())

	if err := dispatcher.ProvideInput(t.Context(), run.ID, response); err != nil {
		t.Fatalf("ProvideInput() error = %v", err)
	}
	waitForInactive(t, dispatcher, run.ID)
	input := fixture.provider.snapshotRequests()[0].Input
	message := input[0].Payload.(alfred.Message)
	if len(input) != 1 || message.Role != alfred.MessageRoleUser || message.Content[0] != response.Content[0] {
		t.Fatalf("restored input = %#v", input)
	}
}

func TestRunnerResumesWithRequestedToolInputAsToolResult(t *testing.T) {
	t.Parallel()

	model := testModel()
	run := testRun(model.Ref)
	run.Status = alfred.RunStatusWaitingForInput
	call := alfred.ToolCall{ID: "tool-call-1", RunID: run.ID, ToolID: "ask"}
	request := alfred.InputRequest{ID: "input-1", CallID: call.ID}
	fixture := newRunnerFixture(t, run, model)
	fixture.events.events = []alfred.RunEvent{
		{RunID: run.ID, Payload: alfred.ToolCallRequested{ModelCallID: "model-call-1", Call: call}},
		{RunID: run.ID, Payload: alfred.ModelCallCompleted{CallID: "model-call-1"}},
		{RunID: run.ID, Payload: alfred.InputRequested{Request: request}},
	}
	fixture.dependencies.Executor = mocks.NewMockExecutor(fixture.controller)
	response := alfred.InputResponse{
		RequestID: request.ID,
		Content:   []alfred.ContentPart{alfred.TextContent{Text: "Friday"}},
	}
	dispatcher := newTestDispatcher(t, fixture.runner())

	if err := dispatcher.ProvideInput(t.Context(), run.ID, response); err != nil {
		t.Fatalf("ProvideInput() error = %v", err)
	}
	waitForInactive(t, dispatcher, run.ID)
	input := fixture.provider.snapshotRequests()[0].Input
	result := input[1].Payload.(alfred.ToolResult)
	if len(input) != 2 || result.CallID != call.ID || result.Output[0] != response.Content[0] {
		t.Fatalf("restored input = %#v", input)
	}
}

func TestRunnerReturnsPolicyDenialAsToolError(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	executor := mocks.NewMockExecutor(controller)
	executor.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(alfred.ExecutionOutcome{
		Decision: alfred.PolicyDecision{Outcome: alfred.PolicyOutcomeDeny},
	}, nil)
	events := &eventState{}
	runner := Runner{executor: executor, events: newEventSinkMock(controller, events)}
	call := alfred.ToolCall{ID: "tool-call-1", RunID: "run-1", ToolID: "shell"}

	results, waiting, err := runner.executeToolCalls(t.Context(), runScope{
		run:     alfred.Run{ID: call.RunID},
		session: alfred.Session{ID: "session-1", ProjectID: "project-1"},
	}, []alfred.ToolCall{call}, nil)
	if err != nil {
		t.Fatalf("executeToolCalls() error = %v", err)
	}
	if waiting || len(results) != 1 || !results[0].IsError || results[0].CallID != call.ID {
		t.Fatalf("results = %#v, waiting = %t", results, waiting)
	}
	var completed alfred.ToolCallCompleted
	for _, event := range events.snapshot() {
		if payload, ok := event.Payload.(alfred.ToolCallCompleted); ok {
			completed = payload
		}
	}
	if !completed.Result.IsError {
		t.Fatal("durable tool result is not marked as an error")
	}
}

type runnerFixture struct {
	controller   *gomock.Controller
	dependencies Dependencies
	runs         *runState
	provider     *providerState
	events       *eventState
	updates      *updateState

	modelContext    alfred.ModelContext
	skillCatalog    alfred.SkillCatalog
	contextRequests []alfred.ContextRequest
	skillProjects   []alfred.ProjectID
}

func newRunnerFixture(t *testing.T, run alfred.Run, model alfred.ModelInfo) *runnerFixture {
	t.Helper()
	controller := gomock.NewController(t)
	fixture := &runnerFixture{
		controller: controller,
		runs:       &runState{run: run},
		provider:   &providerState{providerID: run.DefaultModel.ProviderID, model: model},
		events:     &eventState{},
		updates:    &updateState{},
	}
	runs := newRunStoreMock(controller, fixture.runs)
	sessions := mocks.NewMockSessionStore(controller)
	sessions.EXPECT().GetSession(gomock.Any(), run.SessionID).Return(
		alfred.Session{ID: run.SessionID, ProjectID: "project-1"}, nil,
	).AnyTimes()
	invocations := mocks.NewMockInvocationStore(controller)
	invocations.EXPECT().GetInvocation(gomock.Any(), run.InvocationID).Return(
		alfred.Invocation{ID: run.InvocationID, ProjectID: "project-1"}, nil,
	).AnyTimes()
	provider := newProviderMock(controller, fixture.provider)
	providers := mocks.NewMockProviderResolver(controller)
	providers.EXPECT().ResolveProvider(gomock.Any(), run.DefaultModel.ProviderID).Return(provider, nil).AnyTimes()
	skillSet := mocks.NewMockSkillSet(controller)
	skillSet.EXPECT().Catalog().DoAndReturn(func() alfred.SkillCatalog {
		return fixture.skillCatalog
	}).AnyTimes()
	skills := mocks.NewMockSkillResolver(controller)
	skills.EXPECT().DiscoverSkills(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		projectID alfred.ProjectID,
	) (alfred.SkillSet, error) {
		fixture.skillProjects = append(fixture.skillProjects, projectID)
		return skillSet, nil
	}).AnyTimes()
	contexts := mocks.NewMockContextBuilder(controller)
	contexts.EXPECT().Build(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		request alfred.ContextRequest,
	) (alfred.ModelContext, error) {
		fixture.contextRequests = append(fixture.contextRequests, request)
		return fixture.modelContext, nil
	}).AnyTimes()
	eventSink := newEventSinkMock(controller, fixture.events)
	eventReader := newEventReaderMock(controller, fixture.events)
	broadcaster := mocks.NewMockRunUpdateBroadcaster(controller)
	broadcaster.EXPECT().Publish(gomock.Any(), gomock.Any()).Do(func(
		_ context.Context,
		update alfred.RunUpdate,
	) {
		fixture.updates.add(update)
	}).AnyTimes()
	fixture.dependencies = Dependencies{
		Runs:        runs,
		Sessions:    sessions,
		Invocations: invocations,
		Providers:   providers,
		Skills:      skills,
		Contexts:    contexts,

		Events:      eventSink,
		EventReader: eventReader,
		Broadcaster: broadcaster,
	}
	return fixture
}

func (fixture *runnerFixture) runner() *Runner {
	return NewRunner(fixture.dependencies)
}

type runState struct {
	mu  sync.Mutex
	run alfred.Run
}

func (state *runState) status() alfred.RunStatus {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.run.Status
}

func newRunStoreMock(controller *gomock.Controller, state *runState) *mocks.MockRunStore {
	store := mocks.NewMockRunStore(controller)
	store.EXPECT().GetRun(gomock.Any(), gomock.Any()).DoAndReturn(func(
		context.Context,
		alfred.RunID,
	) (alfred.Run, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.run, nil
	}).AnyTimes()
	store.EXPECT().TransitionRun(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		_ alfred.RunID,
		expected alfred.RunStatus,
		next alfred.RunStatus,
	) error {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.run.Status != expected {
			return errors.B.Op("TransitionRun").KindConflict().Build()
		}
		state.run.Status = next
		return nil
	}).AnyTimes()
	return store
}

type providerState struct {
	mu                  sync.Mutex
	providerID          alfred.ProviderID
	model               alfred.ModelInfo
	events              []alfred.ModelEvent
	streams             [][]alfred.ModelEvent
	streamErr           error
	requests            []alfred.ModelRequest
	started             chan struct{}
	contexts            chan context.Context
	waitForCancellation bool
}

func (state *providerState) snapshotRequests() []alfred.ModelRequest {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]alfred.ModelRequest(nil), state.requests...)
}

func newProviderMock(controller *gomock.Controller, state *providerState) *mocks.MockModelProvider {
	provider := mocks.NewMockModelProvider(controller)
	provider.EXPECT().ID().DoAndReturn(func() alfred.ProviderID { return state.providerID }).AnyTimes()
	provider.EXPECT().ResolveModel(gomock.Any(), gomock.Any()).DoAndReturn(func(
		_ context.Context,
		modelID alfred.ModelID,
	) (alfred.ModelInfo, error) {
		if state.model.Ref.ModelID == "" || state.model.Ref.ModelID != modelID {
			return alfred.ModelInfo{}, errors.B.
				Op("ResolveModel").
				KindNotFound().
				Textf("model %q not found", modelID).
				Build()
		}
		return state.model, nil
	}).AnyTimes()
	provider.EXPECT().Stream(gomock.Any(), gomock.Any()).DoAndReturn(func(
		ctx context.Context,
		request alfred.ModelRequest,
	) iter.Seq2[alfred.ModelEvent, error] {
		state.mu.Lock()
		state.requests = append(state.requests, request)
		streamIndex := len(state.requests) - 1
		state.mu.Unlock()
		if state.contexts != nil {
			state.contexts <- ctx
		}
		return func(yield func(alfred.ModelEvent, error) bool) {
			if state.waitForCancellation {
				close(state.started)
				<-ctx.Done()
				yield(alfred.ModelEvent{}, ctx.Err())
				return
			}
			if state.streamErr != nil {
				yield(alfred.ModelEvent{}, state.streamErr)
				return
			}
			events := state.events
			if streamIndex < len(state.streams) {
				events = state.streams[streamIndex]
			}
			if len(events) == 0 {
				yield(alfred.ModelEvent{
					CallID: request.ID, Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonStop},
				}, nil)
				return
			}
			for _, event := range events {
				if !yield(event, nil) {
					return
				}
			}
		}
	}).AnyTimes()
	return provider
}

type eventState struct {
	mu           sync.Mutex
	events       []alfred.RunEvent
	beforeAppend func(context.Context, alfred.NewRunEvent)
}

func (state *eventState) append(ctx context.Context, event alfred.NewRunEvent) (alfred.RunEvent, error) {
	if state.beforeAppend != nil {
		state.beforeAppend(ctx, event)
	}
	stored := alfred.RunEvent{RunID: event.RunID, Visibility: event.Visibility, Payload: event.Payload}
	state.mu.Lock()
	state.events = append(state.events, stored)
	state.mu.Unlock()
	return stored, nil
}

func (state *eventState) snapshot() []alfred.RunEvent {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]alfred.RunEvent(nil), state.events...)
}

func (state *eventState) read() iter.Seq2[alfred.RunEvent, error] {
	events := state.snapshot()
	return func(yield func(alfred.RunEvent, error) bool) {
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func newEventSinkMock(controller *gomock.Controller, state *eventState) *mocks.MockRunEventSink {
	sink := mocks.NewMockRunEventSink(controller)
	sink.EXPECT().Append(gomock.Any(), gomock.Any()).DoAndReturn(state.append).AnyTimes()
	return sink
}

func newEventReaderMock(controller *gomock.Controller, state *eventState) *mocks.MockRunEventReader {
	reader := mocks.NewMockRunEventReader(controller)
	reader.EXPECT().ReadRunEvents(gomock.Any(), gomock.Any()).DoAndReturn(func(
		context.Context,
		alfred.RunID,
	) iter.Seq2[alfred.RunEvent, error] {
		return state.read()
	}).AnyTimes()
	return reader
}

type updateState struct {
	mu      sync.Mutex
	updates []alfred.RunUpdate
}

func (state *updateState) add(update alfred.RunUpdate) {
	state.mu.Lock()
	state.updates = append(state.updates, update)
	state.mu.Unlock()
}

func (state *updateState) snapshot() []alfred.RunUpdate {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]alfred.RunUpdate(nil), state.updates...)
}

func pendingApprovalEvents(
	runID alfred.RunID,
	call alfred.ToolCall,
	request alfred.ApprovalRequest,
) []alfred.RunEvent {
	return []alfred.RunEvent{
		{RunID: runID, Payload: alfred.ToolCallRequested{ModelCallID: "model-call-1", Call: call}},
		{RunID: runID, Payload: alfred.ModelCallCompleted{CallID: "model-call-1"}},
		{RunID: runID, Payload: alfred.ApprovalRequested{Request: request}},
	}
}

func waitForInactive(t *testing.T, dispatcher *Dispatcher, runID alfred.RunID) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		dispatcher.activeMu.Lock()
		_, active := dispatcher.active[runID]
		dispatcher.activeMu.Unlock()
		if !active {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("run %q remained active", runID)
		case <-ticker.C:
		}
	}
}

func waitForRunEvent[Payload any](t *testing.T, state *eventState) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, event := range state.snapshot() {
			if _, ok := event.Payload.(Payload); ok {
				return
			}
		}
		select {
		case <-deadline.C:
			var payload Payload
			t.Fatalf("run event payload %T was not stored", payload)
		case <-ticker.C:
		}
	}
}

func testModel() alfred.ModelInfo {
	return alfred.ModelInfo{Ref: alfred.ModelRef{ProviderID: "provider-1", ModelID: "model-1"}}
}

func testRun(model alfred.ModelRef) alfred.Run {
	return alfred.Run{
		ID:           "run-1",
		SessionID:    "session-1",
		InvocationID: "invocation-1",
		Status:       alfred.RunStatusQueued,
		DefaultModel: model,
	}
}

func userMessage(text string) alfred.Message {
	return alfred.Message{Role: alfred.MessageRoleUser, Content: []alfred.ContentPart{alfred.TextContent{Text: text}}}
}
