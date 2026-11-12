// Package agent drives Alfred's agent loop.
package agent

import (
	"context"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/internal/history"
	"github.com/ArionMiles/alfred/pkg/identity"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Runner starts or resumes agent runs from durable state.
type Runner struct {
	runs        alfred.RunStore
	sessions    alfred.SessionStore
	invocations alfred.InvocationStore
	providers   alfred.ProviderResolver
	skills      alfred.SkillResolver
	contexts    alfred.ContextBuilder
	executor    alfred.Executor
	events      alfred.RunEventSink
	eventReader alfred.RunEventReader
	broadcaster alfred.RunUpdateBroadcaster
}

// Dependencies contains the harness-owned components used by a Runner.
type Dependencies struct {
	// Loads durable run state and persists lifecycle transitions.
	Runs alfred.RunStore
	// Locates the session and project that own the run.
	Sessions alfred.SessionStore
	// Loads the request that initiated the run.
	Invocations alfred.InvocationStore
	// Resolves the configured model provider for the run.
	Providers alfred.ProviderResolver
	// Discovers skills available to the run's project.
	Skills alfred.SkillResolver
	// Builds model instructions, conversation input, and tool definitions.
	Contexts alfred.ContextBuilder
	// Applies execution policy and invokes requested tools.
	Executor alfred.Executor
	// Persists ordered run events.
	Events alfred.RunEventSink
	// Replays durable events to restore execution state.
	EventReader alfred.RunEventReader
	// Publishes live output and reasoning deltas.
	Broadcaster alfred.RunUpdateBroadcaster
}

type runScope struct {
	run        alfred.Run
	session    alfred.Session
	invocation alfred.Invocation
	provider   alfred.ModelProvider
	model      alfred.ModelInfo
	skills     alfred.SkillSet
}

// NewRunner creates an agent runner from its harness dependencies.
func NewRunner(dependencies Dependencies) *Runner {
	return &Runner{
		runs:        dependencies.Runs,
		sessions:    dependencies.Sessions,
		invocations: dependencies.Invocations,
		providers:   dependencies.Providers,
		skills:      dependencies.Skills,
		contexts:    dependencies.Contexts,
		executor:    dependencies.Executor,
		events:      dependencies.Events,
		eventReader: dependencies.EventReader,
		broadcaster: dependencies.Broadcaster,
	}
}

// Execute prepares the run-scoped dependencies required by the agent loop.
//
//nolint:gocognit // Keeping the run lifecycle in one method makes its durable transition order auditable.
func (runner *Runner) Execute(ctx context.Context, runID alfred.RunID) (executeErr error) {
	const op = "Execute"

	defer func() {
		executeErr = runner.settleExecutionError(ctx, runID, executeErr)
	}()

	scope, err := runner.prepareRun(ctx, runID)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.B.
			Op(op).
			Textf("prepare run %q", runID).
			Err(err).
			Build()
	}
	if scope.run.Status == alfred.RunStatusCancelled {
		return nil
	}
	modelContext, err := runner.contexts.Build(ctx, alfred.ContextRequest{
		ProjectID:    scope.session.ProjectID,
		SessionID:    scope.session.ID,
		RunID:        scope.run.ID,
		Invocation:   scope.invocation,
		Model:        scope.model,
		SkillCatalog: scope.skills.Catalog(),
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.B.
			Op(op).
			Textf("build initial model context for run %q", runID).
			Err(err).
			Build()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := runner.transitionRun(ctx, scope.run.ID, scope.run.Status, alfred.RunStatusRunning); err != nil {
		return errors.B.
			Op(op).
			Textf("start run %q", runID).
			Err(err).
			Build()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	continuation := runContinuation{input: modelContext.Input}
	if scope.run.Status == alfred.RunStatusRunning ||
		scope.run.Status == alfred.RunStatusInterrupted ||
		scope.run.Status == alfred.RunStatusWaitingForApproval ||
		scope.run.Status == alfred.RunStatusWaitingForInput {
		continuation, err = runner.restoreContinuation(ctx, scope.run.ID, modelContext.Input)
		if err != nil {
			return errors.B.
				Op(op).
				Textf("restore continuation for run %q", runID).
				Err(err).
				Build()
		}
	}
	for {
		if len(continuation.pendingToolCalls) > 0 {
			toolResults, waiting, err := runner.executeToolCalls(
				ctx,
				scope,
				continuation.pendingToolCalls,
				continuation.approvalGrants,
			)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return runner.failRun(ctx, runID, err)
			}
			if waiting {
				return nil
			}
			for _, toolResult := range toolResults {
				continuation.input = append(continuation.input, alfred.ModelInput{Payload: toolResult})
			}
			continuation.pendingToolCalls = nil
		}

		modelCallID := identity.New[alfred.ModelCallID]()
		modelRequest := alfred.ModelRequest{
			ID:           modelCallID,
			RunID:        scope.run.ID,
			Model:        scope.run.DefaultModel,
			Instructions: modelContext.Instructions,
			Input:        continuation.input,
			Tools:        modelContext.Tools,
		}

		result, callErr := runner.streamModel(ctx, scope.session.ID, scope.provider, modelRequest)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if callErr != nil {
			if errors.IsRetryable(callErr) {
				return runner.interruptRun(ctx, runID, callErr)
			}
			return runner.failRun(ctx, runID, errors.B.
				Op(op).
				Textf("execute model call %q for run %q", modelRequest.ID, runID).
				Err(callErr).
				Build())
		}
		if err := runner.recordModelCallCompletion(ctx, runID, modelRequest.ID, result.output); err != nil {
			return runner.failRun(ctx, runID, err)
		}

		for _, item := range result.output {
			if message, ok := item.Payload.(alfred.Message); ok {
				if err := runner.recordMessage(ctx, runID, modelRequest.ID, message); err != nil {
					return runner.failRun(ctx, runID, err)
				}
			}
		}
		continuation.input = append(continuation.input, result.output...)

		calls := result.toolCalls()
		if len(calls) == 0 {
			if err := runner.completeRun(ctx, scope.run.ID); err != nil {
				return errors.B.
					Op(op).
					Textf("complete run %q", runID).
					Err(err).
					Build()
			}
			return nil
		}

		toolResults, waiting, err := runner.executeToolCalls(ctx, scope, calls, nil)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return runner.failRun(ctx, runID, err)
		}
		if waiting {
			return nil
		}
		for _, toolResult := range toolResults {
			continuation.input = append(continuation.input, alfred.ModelInput{Payload: toolResult})
		}
	}
}

// settleExecutionError prevents an asynchronously executed run from remaining
// queued or running when execution ends with an error. Waiting, interrupted,
// and terminal states already describe a durable outcome and are left intact.
func (runner *Runner) settleExecutionError(
	ctx context.Context,
	runID alfred.RunID,
	cause error,
) error {
	const op = "settleExecutionError"

	if cause == nil || ctx.Err() != nil {
		return cause
	}
	settleContext := context.WithoutCancel(ctx)
	run, err := runner.runs.GetRun(settleContext, runID)
	if err != nil {
		return errors.B.
			Op(op).
			Textf("get run %q after execution failed", runID).
			Err(cause).
			Err(err).
			Build()
	}
	if run.Status != alfred.RunStatusQueued && run.Status != alfred.RunStatusRunning {
		return cause
	}
	if err := runner.transitionRun(
		settleContext,
		runID,
		run.Status,
		alfred.RunStatusFailed,
	); err != nil {
		return errors.B.
			Op(op).
			Textf("record failed execution for run %q", runID).
			Err(cause).
			Err(err).
			Build()
	}
	return cause
}

type modelCallResult struct {
	output []alfred.ModelInput
}

func (result modelCallResult) toolCalls() []alfred.ToolCall {
	var calls []alfred.ToolCall
	for _, item := range result.output {
		if call, ok := item.Payload.(alfred.ToolCall); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

type runContinuation struct {
	input            []alfred.ModelInput
	pendingToolCalls []alfred.ToolCall
	approvalGrants   map[alfred.ToolCallID]alfred.ApprovalGrant
}

func (runner *Runner) streamModel(
	ctx context.Context,
	sessionID alfred.SessionID,
	provider alfred.ModelProvider,
	request alfred.ModelRequest,
) (modelCallResult, error) {
	const op = "streamModel"

	result := modelCallResult{output: make([]alfred.ModelInput, 0)}
	var completed bool
	var pendingMessage bool
	var pendingReasoning bool

	for event, err := range provider.Stream(ctx, request) {
		if err != nil {
			return result, errors.B.
				Op(op).
				Textf("read model call %q stream", request.ID).
				Err(err).
				Build()
		}

		switch payload := event.Payload.(type) {
		case alfred.ModelContentDelta:
			pendingMessage = true
			runner.broadcaster.Publish(ctx, alfred.RunUpdate{
				SessionID:  sessionID,
				RunID:      request.RunID,
				Visibility: alfred.RunEventVisibilityUser,
				Payload: alfred.RunContentDelta{
					CallID: request.ID,
					Delta:  payload,
				},
			})
		case alfred.ModelMessage:
			if payload.Message.Role != alfred.MessageRoleAssistant {
				return result, errors.B.Op(op).KindBadGateway().UserMsg("the model provider returned an invalid message").Build()
			}
			result.output = append(result.output, alfred.ModelInput{Payload: payload.Message, Native: event.Native})
			pendingMessage = false
		case alfred.ModelReasoningDelta:
			pendingReasoning = true
			runner.broadcaster.Publish(ctx, alfred.RunUpdate{
				SessionID: sessionID, RunID: request.RunID, Visibility: alfred.RunEventVisibilityUser,
				Payload: alfred.RunReasoningDelta{CallID: request.ID, Delta: payload},
			})
		case alfred.ModelReasoning:
			result.output = append(result.output, alfred.ModelInput{Payload: payload.Reasoning, Native: event.Native})
			pendingReasoning = false
		case alfred.ModelToolCall:
			call := payload.Call
			call.RunID = request.RunID
			result.output = append(result.output, alfred.ModelInput{Payload: call, Native: event.Native})
			if _, err := runner.events.Append(ctx, alfred.NewRunEvent{
				RunID:      request.RunID,
				Visibility: alfred.RunEventVisibilityUser,
				Payload: alfred.ToolCallRequested{
					ModelCallID: request.ID,
					Call:        call,
				},
			}); err != nil {
				return result, errors.B.
					Op(op).
					Textf("record tool call %q for run %q", call.ID, request.RunID).
					Err(err).
					Build()
			}
		case alfred.ModelUsage:
		case alfred.ModelCompleted:
			completed = true
		}
	}

	if !completed || pendingMessage || pendingReasoning {
		return result, errors.B.
			Op(op).
			KindBadGateway().
			Textf("model call %q stream did not finish its output", request.ID).
			UserMsg("the model provider returned an incomplete response").
			Build()
	}

	return result, nil
}

func (runner *Runner) restoreContinuation(
	ctx context.Context,
	runID alfred.RunID,
	baseInput []alfred.ModelInput,
) (runContinuation, error) {
	const op = "restoreContinuation"

	projection := history.New(baseInput)
	for event, err := range runner.eventReader.ReadRunEvents(ctx, runID) {
		if err != nil {
			return runContinuation{}, errors.B.
				Op(op).
				Textf("read events for run %q", runID).
				Err(err).
				Build()
		}

		// The builder already supplied this run's invocation message.
		if added, ok := event.Payload.(alfred.MessageAdded); ok && added.Message.Role == alfred.MessageRoleUser {
			continue
		}
		projection.Apply(event)
	}
	return runContinuation{
		input: projection.Input, pendingToolCalls: projection.PendingCalls(), approvalGrants: projection.ApprovalGrants,
	}, nil
}

func (runner *Runner) recordMessage(
	ctx context.Context,
	runID alfred.RunID,
	callID alfred.ModelCallID,
	message alfred.Message,
) error {
	const op = "recordMessage"

	_, err := runner.events.Append(ctx, alfred.NewRunEvent{
		RunID:      runID,
		Visibility: alfred.RunEventVisibilityUser,
		Payload:    alfred.MessageAdded{Message: message, ModelCallID: callID},
	})
	if err != nil {
		return errors.B.
			Op(op).
			Textf("record %q message for run %q", message.Role, runID).
			Err(err).
			Build()
	}

	return nil
}

func (runner *Runner) recordModelCallCompletion(
	ctx context.Context,
	runID alfred.RunID,
	callID alfred.ModelCallID,
	output []alfred.ModelInput,
) error {
	const op = "recordModelCallCompletion"

	_, err := runner.events.Append(ctx, alfred.NewRunEvent{
		RunID:      runID,
		Visibility: alfred.RunEventVisibilityInternal,
		Payload:    alfred.ModelCallCompleted{CallID: callID, Output: output},
	})
	if err != nil {
		return errors.B.
			Op(op).
			Textf("record completion of model call %q", callID).
			Err(err).
			Build()
	}
	return nil
}

//nolint:gocognit // Tool execution keeps approval and durable-event ordering visible in one flow.
func (runner *Runner) executeToolCalls(
	ctx context.Context,
	scope runScope,
	calls []alfred.ToolCall,
	grants map[alfred.ToolCallID]alfred.ApprovalGrant,
) ([]alfred.ToolResult, bool, error) {
	const op = "executeToolCalls"

	results := make([]alfred.ToolResult, 0, len(calls))
	for _, call := range calls {
		var grant *alfred.ApprovalGrant
		if value, ok := grants[call.ID]; ok {
			grant = &value
		}
		outcome, err := runner.executor.Execute(ctx, alfred.ExecutionRequest{
			ProjectID: scope.session.ProjectID,
			SessionID: scope.session.ID,
			Call:      call,
			Approval:  grant,
		})
		if err != nil {
			return nil, false, errors.B.
				Op(op).
				Textf("execute tool call %q for run %q", call.ID, scope.run.ID).
				Err(err).
				Build()
		}

		if _, err := runner.events.Append(ctx, alfred.NewRunEvent{
			RunID:      scope.run.ID,
			Visibility: alfred.RunEventVisibilityInternal,
			Payload: alfred.PolicyEvaluated{
				CallID:   call.ID,
				Decision: outcome.Decision,
			},
		}); err != nil {
			return nil, false, errors.B.
				Op(op).
				Textf("record policy decision for tool call %q", call.ID).
				Err(err).
				Build()
		}

		switch outcome.Decision.Outcome {
		case alfred.PolicyOutcomeAllow:
			if len(outcome.InputPrompt) != 0 {
				return results, true, runner.requestToolInput(ctx, scope.run.ID, call.ID, outcome.InputPrompt)
			}
			result := *outcome.Result
			if err := runner.recordToolResult(ctx, scope.run.ID, result); err != nil {
				return nil, false, err
			}
			results = append(results, result)
		case alfred.PolicyOutcomeDeny:
			result := alfred.ToolResult{
				CallID: call.ID,
				Output: []alfred.ContentPart{
					alfred.TextContent{Text: "Tool execution was denied by policy."},
				},
				IsError: true,
			}
			if err := runner.recordToolResult(ctx, scope.run.ID, result); err != nil {
				return nil, false, err
			}
			results = append(results, result)
		case alfred.PolicyOutcomeAsk:
			approvalID := identity.New[alfred.ApprovalID]()
			if _, err := runner.events.Append(ctx, alfred.NewRunEvent{
				RunID:      scope.run.ID,
				Visibility: alfred.RunEventVisibilityUser,
				Payload: alfred.ApprovalRequested{Request: alfred.ApprovalRequest{
					ID:           approvalID,
					CallID:       call.ID,
					ActionDigest: outcome.ActionDigest,
					Reason:       outcome.Decision.Reason,
				}},
			}); err != nil {
				return nil, false, errors.B.
					Op(op).
					Textf("record approval request %q for tool call %q", approvalID, call.ID).
					Err(err).
					Build()
			}
			if err := runner.transitionRun(
				ctx,
				scope.run.ID,
				alfred.RunStatusRunning,
				alfred.RunStatusWaitingForApproval,
			); err != nil {
				return nil, false, err
			}
			return results, true, nil
		}
	}

	return results, false, nil
}

func (runner *Runner) resolveApproval(
	ctx context.Context,
	runID alfred.RunID,
	resolution alfred.ApprovalResolution,
) error {
	const op = "resolveApproval"

	run, err := runner.runs.GetRun(ctx, runID)
	if err != nil {
		return errors.B.
			Op(op).
			Textf("get run %q", runID).
			Err(err).
			Build()
	}
	if run.Status != alfred.RunStatusWaitingForApproval {
		return errors.B.
			Op(op).
			KindConflict().
			Textf("run %q has status %q", runID, run.Status).
			UserMsg("this run is not waiting for approval").
			Build()
	}
	if resolution.Outcome != alfred.ApprovalOutcomeApproved &&
		resolution.Outcome != alfred.ApprovalOutcomeDenied {
		return errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("approval %q has outcome %q", resolution.ApprovalID, resolution.Outcome).
			UserMsg("choose whether to approve or deny the request").
			Build()
	}
	if err := runner.requirePendingRequest(
		ctx,
		runID,
		string(resolution.ApprovalID),
		func(payload alfred.RunEventPayload) (bool, bool) {
			switch payload := payload.(type) {
			case alfred.ApprovalRequested:
				return payload.Request.ID == resolution.ApprovalID, false
			case alfred.ApprovalResolved:
				return false, payload.Resolution.ApprovalID == resolution.ApprovalID
			default:
				return false, false
			}
		},
	); err != nil {
		return errors.B.
			Op(op).
			Textf("resolve approval %q for run %q", resolution.ApprovalID, runID).
			Err(err).
			Build()
	}
	if _, err := runner.events.Append(ctx, alfred.NewRunEvent{
		RunID:      runID,
		Visibility: alfred.RunEventVisibilityUser,
		Payload:    alfred.ApprovalResolved{Resolution: resolution},
	}); err != nil {
		return errors.B.
			Op(op).
			Textf("record resolution for approval %q", resolution.ApprovalID).
			Err(err).
			Build()
	}

	return nil
}

func (runner *Runner) provideInput(
	ctx context.Context,
	runID alfred.RunID,
	response alfred.InputResponse,
) error {
	const op = "provideInput"

	run, err := runner.runs.GetRun(ctx, runID)
	if err != nil {
		return errors.B.
			Op(op).
			Textf("get run %q", runID).
			Err(err).
			Build()
	}
	if run.Status != alfred.RunStatusWaitingForInput {
		return errors.B.
			Op(op).
			KindConflict().
			Textf("run %q has status %q", runID, run.Status).
			UserMsg("this run is not waiting for input").
			Build()
	}
	if err := runner.requirePendingRequest(
		ctx,
		runID,
		string(response.RequestID),
		func(payload alfred.RunEventPayload) (bool, bool) {
			switch payload := payload.(type) {
			case alfred.InputRequested:
				return payload.Request.ID == response.RequestID, false
			case alfred.InputReceived:
				return false, payload.Response.RequestID == response.RequestID
			default:
				return false, false
			}
		},
	); err != nil {
		return errors.B.
			Op(op).
			Textf("provide input for request %q in run %q", response.RequestID, runID).
			Err(err).
			Build()
	}
	if _, err := runner.events.Append(ctx, alfred.NewRunEvent{
		RunID:      runID,
		Visibility: alfred.RunEventVisibilityUser,
		Payload:    alfred.InputReceived{Response: response},
	}); err != nil {
		return errors.B.
			Op(op).
			Textf("record input for request %q", response.RequestID).
			Err(err).
			Build()
	}

	return nil
}

func (runner *Runner) resumeRun(ctx context.Context, runID alfred.RunID) error {
	const op = "resumeRun"

	run, err := runner.runs.GetRun(ctx, runID)
	if err != nil {
		return errors.B.
			Op(op).
			Textf("get run %q", runID).
			Err(err).
			Build()
	}
	if run.Status != alfred.RunStatusInterrupted {
		return errors.B.
			Op(op).
			KindConflict().
			Textf("run %q has status %q", runID, run.Status).
			UserMsg("this run is not interrupted").
			Build()
	}

	return nil
}

func (runner *Runner) requirePendingRequest(
	ctx context.Context,
	runID alfred.RunID,
	requestID string,
	inspect func(alfred.RunEventPayload) (bool, bool),
) error {
	const op = "requirePendingRequest"

	var requested, resolved bool
	err := runner.visitRunEvents(ctx, runID, func(payload alfred.RunEventPayload) {
		payloadRequested, payloadResolved := inspect(payload)
		requested = requested || payloadRequested
		resolved = resolved || payloadResolved
	})
	if err != nil {
		return errors.B.
			Op(op).
			Textf("read events for run %q", runID).
			Err(err).
			Build()
	}
	if !requested {
		return errors.B.
			Op(op).
			KindNotFound().
			Textf("request %q does not exist in run %q", requestID, runID).
			UserMsg("the request could not be found").
			Build()
	}
	if resolved {
		return errors.B.
			Op(op).
			KindConflict().
			Textf("request %q in run %q is already resolved", requestID, runID).
			UserMsg("this request has already been resolved").
			Build()
	}

	return nil
}

func (runner *Runner) visitRunEvents(
	ctx context.Context,
	runID alfred.RunID,
	visit func(alfred.RunEventPayload),
) error {
	const op = "visitRunEvents"

	for event, err := range runner.eventReader.ReadRunEvents(ctx, runID) {
		if err != nil {
			return errors.B.
				Op(op).
				Textf("read events for run %q", runID).
				Err(err).
				Build()
		}
		visit(event.Payload)
	}

	return nil
}

func (runner *Runner) recordToolResult(
	ctx context.Context,
	runID alfred.RunID,
	result alfred.ToolResult,
) error {
	const op = "recordToolResult"

	_, err := runner.events.Append(ctx, alfred.NewRunEvent{
		RunID:      runID,
		Visibility: alfred.RunEventVisibilityUser,
		Payload:    alfred.ToolCallCompleted{Result: result},
	})
	if err != nil {
		return errors.B.
			Op(op).
			Textf("record result for tool call %q", result.CallID).
			Err(err).
			Build()
	}

	return nil
}

func (runner *Runner) completeRun(ctx context.Context, runID alfred.RunID) error {
	const op = "completeRun"

	if err := runner.transitionRun(
		ctx,
		runID,
		alfred.RunStatusRunning,
		alfred.RunStatusCompleted,
	); err != nil {
		return errors.B.
			Op(op).
			Textf("complete run %q", runID).
			Err(err).
			Build()
	}

	return nil
}

func (runner *Runner) failRun(ctx context.Context, runID alfred.RunID, cause error) error {
	const op = "failRun"

	return errors.B.
		Op(op).
		Textf("fail run %q", runID).
		Err(cause).
		Err(runner.transitionRun(
			ctx,
			runID,
			alfred.RunStatusRunning,
			alfred.RunStatusFailed,
		)).
		Build()
}

func (runner *Runner) interruptRun(
	ctx context.Context,
	runID alfred.RunID,
	cause error,
) error {
	const op = "interruptRun"

	if _, err := runner.events.Append(ctx, alfred.NewRunEvent{
		RunID:      runID,
		Visibility: alfred.RunEventVisibilityUser,
		Payload: alfred.RunInterrupted{
			Reason:  errors.WhatKind(cause).Code,
			Message: errors.UserMsg(cause),
		},
	}); err != nil {
		return errors.B.
			Op(op).
			Textf("record interruption for run %q", runID).
			Err(err).
			Build()
	}
	if err := runner.transitionRun(
		ctx,
		runID,
		alfred.RunStatusRunning,
		alfred.RunStatusInterrupted,
	); err != nil {
		return errors.B.
			Op(op).
			Textf("interrupt run %q", runID).
			Err(err).
			Build()
	}

	return nil
}

func (runner *Runner) transitionRun(
	ctx context.Context,
	runID alfred.RunID,
	expected alfred.RunStatus,
	next alfred.RunStatus,
) error {
	const op = "transitionRun"

	if err := runner.runs.TransitionRun(ctx, runID, expected, next); err != nil {
		return errors.B.
			Op(op).
			Textf("transition run %q from %q to %q", runID, expected, next).
			Err(err).
			Build()
	}
	if _, err := runner.events.Append(ctx, alfred.NewRunEvent{
		RunID:      runID,
		Visibility: alfred.RunEventVisibilityUser,
		Payload:    alfred.RunStateChanged{Status: next},
	}); err != nil {
		return errors.B.
			Op(op).
			Textf("record status %q for run %q", next, runID).
			Err(err).
			Build()
	}

	return nil
}

func (runner *Runner) prepareRun(
	ctx context.Context,
	runID alfred.RunID,
) (runScope, error) {
	const op = "prepareRun"

	run, err := runner.runs.GetRun(ctx, runID)
	if err != nil {
		return runScope{}, errors.B.
			Op(op).
			Textf("get run %q", runID).
			Err(err).
			Build()
	}
	session, err := runner.sessions.GetSession(ctx, run.SessionID)
	if err != nil {
		return runScope{}, errors.B.
			Op(op).
			Textf("get session %q for run %q", run.SessionID, runID).
			Err(err).
			Build()
	}
	invocation, err := runner.invocations.GetInvocation(ctx, run.InvocationID)
	if err != nil {
		return runScope{}, errors.B.
			Op(op).
			Textf("get invocation %q for run %q", run.InvocationID, runID).
			Err(err).
			Build()
	}
	provider, err := runner.providers.ResolveProvider(ctx, run.DefaultModel.ProviderID)
	if err != nil {
		return runScope{}, errors.B.
			Op(op).
			Textf("resolve provider %q for run %q", run.DefaultModel.ProviderID, runID).
			Err(err).
			Build()
	}
	model, err := provider.ResolveModel(ctx, run.DefaultModel.ModelID)
	if err != nil {
		return runScope{}, errors.B.
			Op(op).
			Textf(
				"resolve model %q from provider %q for run %q",
				run.DefaultModel.ModelID,
				provider.ID(),
				runID,
			).
			Err(err).
			Build()
	}
	skillSet, err := runner.skills.DiscoverSkills(ctx, session.ProjectID)
	if err != nil {
		return runScope{}, errors.B.
			Op(op).
			Textf("discover skills for run %q in project %q", runID, session.ProjectID).
			Err(err).
			Build()
	}

	return runScope{
		run:        run,
		session:    session,
		invocation: invocation,
		provider:   provider,
		model:      model,
		skills:     skillSet,
	}, nil
}

var _ alfred.AgentRunner = (*Runner)(nil)
