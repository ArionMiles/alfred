// Package history projects durable events into model input and recovery state.
package history

import "github.com/ArionMiles/alfred"

// Projection retains committed model input and facts needed to resume tool work.
// It does not read storage or execute tools.
type Projection struct {
	Input          []alfred.ModelInput
	ApprovalGrants map[alfred.ToolCallID]alfred.ApprovalGrant
	requested      map[alfred.ModelCallID][]alfred.ToolCall
	pending        map[alfred.ToolCallID]bool
	approvals      map[alfred.ApprovalID]alfred.ApprovalRequest
	inputs         map[alfred.InputRequestID]alfred.InputRequest
}

// New starts a projection with an owned copy of the initial model input.
func New(input []alfred.ModelInput) *Projection {
	return &Projection{
		Input:          append([]alfred.ModelInput(nil), input...),
		ApprovalGrants: make(map[alfred.ToolCallID]alfred.ApprovalGrant),
		requested:      make(map[alfred.ModelCallID][]alfred.ToolCall),
		pending:        make(map[alfred.ToolCallID]bool),
		approvals:      make(map[alfred.ApprovalID]alfred.ApprovalRequest),
		inputs:         make(map[alfred.InputRequestID]alfred.InputRequest),
	}
}

// Apply consumes one event in durable append order. Tool requests become model
// input only after their model call's completion checkpoint has been recorded.
func (projection *Projection) Apply(event alfred.RunEvent) {
	switch payload := event.Payload.(type) {
	case alfred.MessageAdded:
		if payload.ModelCallID == "" {
			projection.Input = append(projection.Input, alfred.ModelInput{Payload: payload.Message})
		}
	case alfred.ToolCallRequested:
		projection.requested[payload.ModelCallID] = append(projection.requested[payload.ModelCallID], payload.Call)
	case alfred.ModelCallCompleted:
		projection.completeModelCall(payload)
	case alfred.ToolCallCompleted:
		projection.resolve(payload.Result)
	case alfred.ApprovalRequested:
		projection.approvals[payload.Request.ID] = payload.Request
	case alfred.ApprovalResolved:
		projection.applyApproval(payload.Resolution)
	case alfred.InputRequested:
		projection.inputs[payload.Request.ID] = payload.Request
	case alfred.InputReceived:
		projection.applyInput(payload.Response)
	}
}

func (projection *Projection) completeModelCall(completed alfred.ModelCallCompleted) {
	output := completed.Output
	if output == nil {
		// Older checkpoints committed tool requests without an output snapshot.
		for _, call := range projection.requested[completed.CallID] {
			output = append(output, alfred.ModelInput{Payload: call})
		}
	}
	projection.Input = append(projection.Input, output...)
	for _, item := range output {
		if call, ok := item.Payload.(alfred.ToolCall); ok {
			projection.pending[call.ID] = true
		}
	}
	delete(projection.requested, completed.CallID)
}

func (projection *Projection) applyApproval(resolution alfred.ApprovalResolution) {
	request, ok := projection.approvals[resolution.ApprovalID]
	if !ok || !projection.pending[request.CallID] {
		return
	}
	switch resolution.Outcome {
	case alfred.ApprovalOutcomeApproved:
		projection.ApprovalGrants[request.CallID] = alfred.ApprovalGrant{
			ApprovalID: request.ID, ActionDigest: request.ActionDigest,
		}
	case alfred.ApprovalOutcomeDenied:
		projection.resolve(deniedResult(request))
	}
}

func (projection *Projection) applyInput(response alfred.InputResponse) {
	request, ok := projection.inputs[response.RequestID]
	if !ok {
		return
	}
	if request.CallID == "" {
		projection.Input = append(projection.Input, alfred.ModelInput{Payload: alfred.Message{Role: alfred.MessageRoleUser, Content: response.Content}})
		return
	}
	projection.resolve(alfred.ToolResult{CallID: request.CallID, Output: response.Content})
}

func (projection *Projection) resolve(result alfred.ToolResult) {
	if !projection.pending[result.CallID] {
		return
	}
	projection.Input = append(projection.Input, alfred.ModelInput{Payload: result})
	delete(projection.pending, result.CallID)
	delete(projection.ApprovalGrants, result.CallID)
}

// PendingCalls returns committed, unresolved tool calls in their original order.
// The runner decides whether and how to execute them.
func (projection *Projection) PendingCalls() []alfred.ToolCall {
	var calls []alfred.ToolCall
	for _, input := range projection.Input {
		if call, ok := input.Payload.(alfred.ToolCall); ok && projection.pending[call.ID] {
			calls = append(calls, call)
		}
	}
	return calls
}

// ResolvedInput includes messages and resolved tool exchanges for earlier-session
// context. Unresolved calls are omitted rather than scheduled as new work.
func (projection *Projection) ResolvedInput() []alfred.ModelInput {
	input := make([]alfred.ModelInput, 0, len(projection.Input))
	for _, item := range projection.Input {
		if call, ok := item.Payload.(alfred.ToolCall); ok && projection.pending[call.ID] {
			continue
		}
		input = append(input, item)
	}
	return input
}

func deniedResult(request alfred.ApprovalRequest) alfred.ToolResult {
	return alfred.ToolResult{
		CallID:  request.CallID,
		Output:  []alfred.ContentPart{alfred.TextContent{Text: "Tool execution was denied by the user."}},
		IsError: true,
	}
}
