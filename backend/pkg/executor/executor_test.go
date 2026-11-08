package executor

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/tools"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type testTool struct {
	definition alfred.ToolDefinition
	invoked    bool
}

func (tool *testTool) Definition() alfred.ToolDefinition {
	return tool.definition
}

func (tool *testTool) Invoke(_ context.Context, invocation alfred.ToolInvocation) (alfred.ToolOutcome, error) {
	tool.invoked = true
	return alfred.ToolOutcome{Result: &alfred.ToolResult{CallID: invocation.Call.ID}}, nil
}

type policyManagerFunc func(context.Context, alfred.AuthorizationRequest) (alfred.PolicyDecision, error)

func (evaluate policyManagerFunc) Evaluate(
	ctx context.Context,
	request alfred.AuthorizationRequest,
) (alfred.PolicyDecision, error) {
	return evaluate(ctx, request)
}

func TestExecutorAllowsToolByDefault(t *testing.T) {
	tool := &testTool{definition: alfred.ToolDefinition{ID: "read"}}
	registry, err := tools.NewRegistry(tool)
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}

	executor := New(registry, testProjects(t))
	outcome, err := executor.Execute(t.Context(), alfred.ExecutionRequest{
		Call: alfred.ToolCall{ID: "call-1", ToolID: "read"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !tool.invoked {
		t.Fatal("tool was not invoked")
	}
	if outcome.Decision.Outcome != alfred.PolicyOutcomeAllow {
		t.Fatalf("Execute() outcome = %q, want %q", outcome.Decision.Outcome, alfred.PolicyOutcomeAllow)
	}
}

func TestExecutorSuppliesTrustedProjectRoot(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	projects := mocks.NewMockProjectStore(controller)
	projects.EXPECT().GetProject(gomock.Any(), alfred.ProjectID("project")).Return(alfred.Project{Root: "/trusted/project"}, nil)
	tool := mocks.NewMockTool(controller)
	tool.EXPECT().Definition().Return(alfred.ToolDefinition{ID: "read"}).AnyTimes()
	call := alfred.ToolCall{ID: "call", ToolID: "read"}
	tool.EXPECT().Invoke(gomock.Any(), alfred.ToolInvocation{Call: call, ProjectRoot: "/trusted/project"}).Return(
		alfred.ToolOutcome{Result: &alfred.ToolResult{CallID: call.ID}}, nil,
	)
	registry, err := tools.NewRegistry(tool)
	if err != nil {
		t.Fatal(err)
	}
	result, err := New(registry, projects).Execute(t.Context(), alfred.ExecutionRequest{ProjectID: "project", Call: call})
	if err != nil || result.Result == nil || result.Result.CallID != call.ID {
		t.Fatalf("outcome=%v error=%v", result, err)
	}
}

func TestExecutorInvokesAllowedTool(t *testing.T) {
	tool := &testTool{definition: alfred.ToolDefinition{ID: "read"}}
	registry, err := tools.NewRegistry(tool)
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}

	var authorization alfred.AuthorizationRequest
	executor := New(registry, testProjects(t), WithPolicyManager(policyManagerFunc(
		func(_ context.Context, request alfred.AuthorizationRequest) (alfred.PolicyDecision, error) {
			authorization = request
			return alfred.PolicyDecision{Outcome: alfred.PolicyOutcomeAllow}, nil
		},
	)))

	request := alfred.ExecutionRequest{
		ProjectID: "project-1",
		SessionID: "session-1",
		Call: alfred.ToolCall{
			ID:        "call-1",
			RunID:     "run-1",
			ToolID:    "read",
			Arguments: json.RawMessage(`{"path":"notes.md"}`),
		},
	}
	request.Approval = &alfred.ApprovalGrant{
		ApprovalID:   "approval-1",
		ActionDigest: actionDigest(request.Call),
	}
	outcome, err := executor.Execute(t.Context(), request)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !tool.invoked {
		t.Fatal("tool was not invoked")
	}
	if outcome.Result == nil || outcome.Result.CallID != request.Call.ID {
		t.Fatalf("Execute() result = %#v, want call ID %q", outcome.Result, request.Call.ID)
	}
	if outcome.ActionDigest == "" {
		t.Fatal("Execute() action digest is empty")
	}
	if authorization.ProjectID != request.ProjectID || authorization.SessionID != request.SessionID {
		t.Fatalf("authorization context = (%q, %q), want (%q, %q)", authorization.ProjectID, authorization.SessionID, request.ProjectID, request.SessionID)
	}
	if authorization.Call.ID != request.Call.ID || authorization.Tool.ID != request.Call.ToolID {
		t.Fatalf("authorization action = %#v, want call %q and tool %q", authorization, request.Call.ID, request.Call.ToolID)
	}
	if authorization.Approval != request.Approval {
		t.Fatalf("authorization approval = %#v, want %#v", authorization.Approval, request.Approval)
	}
}

func TestExecutorRejectsApprovalForDifferentAction(t *testing.T) {
	tool := &testTool{definition: alfred.ToolDefinition{ID: "shell"}}
	registry, err := tools.NewRegistry(tool)
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}

	policyCalled := false
	executor := New(registry, testProjects(t), WithPolicyManager(policyManagerFunc(
		func(context.Context, alfred.AuthorizationRequest) (alfred.PolicyDecision, error) {
			policyCalled = true
			return alfred.PolicyDecision{Outcome: alfred.PolicyOutcomeAllow}, nil
		},
	)))

	_, err = executor.Execute(t.Context(), alfred.ExecutionRequest{
		Call: alfred.ToolCall{ID: "call-1", ToolID: "shell"},
		Approval: &alfred.ApprovalGrant{
			ApprovalID:   "approval-1",
			ActionDigest: "digest-for-another-action",
		},
	})
	if kind := errors.WhatKind(err); kind != errors.PermissionDenied {
		t.Fatalf("errors.WhatKind(error) = %#v, want %#v", kind, errors.PermissionDenied)
	}
	if policyCalled {
		t.Fatal("policy manager was called for mismatched approval")
	}
	if tool.invoked {
		t.Fatal("tool was invoked for mismatched approval")
	}
}

func TestExecutorDoesNotInvokeToolWhenPolicyStopsExecution(t *testing.T) {
	for _, outcome := range []alfred.PolicyOutcome{
		alfred.PolicyOutcomeDeny,
		alfred.PolicyOutcomeAsk,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			tool := &testTool{definition: alfred.ToolDefinition{ID: "shell"}}
			registry, err := tools.NewRegistry(tool)
			if err != nil {
				t.Fatalf("tools.NewRegistry() error = %v", err)
			}
			executor := New(registry, testProjects(t), WithPolicyManager(policyManagerFunc(
				func(context.Context, alfred.AuthorizationRequest) (alfred.PolicyDecision, error) {
					return alfred.PolicyDecision{Outcome: outcome}, nil
				},
			)))

			got, err := executor.Execute(t.Context(), alfred.ExecutionRequest{
				Call: alfred.ToolCall{ID: "call-1", ToolID: "shell"},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if tool.invoked {
				t.Fatal("tool was invoked")
			}
			if got.Decision.Outcome != outcome {
				t.Fatalf("Execute() outcome = %q, want %q", got.Decision.Outcome, outcome)
			}
			if got.Result != nil {
				t.Fatalf("Execute() result = %#v, want nil", got.Result)
			}
			if got.ActionDigest == "" {
				t.Fatal("Execute() action digest is empty")
			}
		})
	}
}

func testProjects(t *testing.T) *mocks.MockProjectStore {
	t.Helper()
	projects := mocks.NewMockProjectStore(gomock.NewController(t))
	projects.EXPECT().GetProject(gomock.Any(), gomock.Any()).Return(alfred.Project{Root: t.TempDir()}, nil).AnyTimes()
	return projects
}
