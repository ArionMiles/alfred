// Package executor provides Alfred's policy-enforced tool execution boundary.
package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/policy"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Option configures an Executor.
type Option func(*Executor)

// WithPolicyManager configures the policy manager used before tool invocation.
func WithPolicyManager(manager alfred.PolicyManager) Option {
	return func(executor *Executor) {
		executor.policyManager = manager
	}
}

// Executor resolves tools and invokes those allowed by policy.
type Executor struct {
	tools         alfred.ToolResolver
	projects      projectReader
	policyManager alfred.PolicyManager
}

type projectReader interface {
	GetProject(context.Context, alfred.ProjectID) (alfred.Project, error)
}

// New creates a policy-enforced executor that allows actions by default.
func New(tools alfred.ToolResolver, projects projectReader, options ...Option) *Executor {
	executor := &Executor{
		tools:    tools,
		projects: projects,
		policyManager: policy.NewFixedManager(alfred.PolicyDecision{
			Outcome: alfred.PolicyOutcomeAllow,
		}),
	}
	for _, option := range options {
		option(executor)
	}
	return executor
}

// Execute evaluates policy and invokes an allowed tool call.
func (executor *Executor) Execute(
	ctx context.Context,
	request alfred.ExecutionRequest,
) (alfred.ExecutionOutcome, error) {
	const op = "Execute"

	tool, err := executor.tools.ResolveTool(ctx, request.Call.ToolID)
	if err != nil {
		return alfred.ExecutionOutcome{}, errors.B.Op(op).Err(err).Build()
	}

	outcome := alfred.ExecutionOutcome{
		ActionDigest: actionDigest(request.Call),
	}
	if request.Approval != nil && request.Approval.ActionDigest != outcome.ActionDigest {
		return outcome, errors.B.
			Op(op).
			KindPermissionDenied().
			Textf("approval %q does not match tool call %q", request.Approval.ApprovalID, request.Call.ID).
			UserMsg("the approval does not match the requested action").
			Build()
	}
	outcome.Decision, err = executor.policyManager.Evaluate(ctx, alfred.AuthorizationRequest{
		ProjectID: request.ProjectID,
		SessionID: request.SessionID,
		Tool:      tool.Definition(),
		Call:      request.Call,
		Approval:  request.Approval,
	})
	if err != nil {
		return outcome, errors.B.Op(op).Err(err).Build()
	}

	switch outcome.Decision.Outcome {
	case alfred.PolicyOutcomeAllow:
		project, projectErr := executor.projects.GetProject(ctx, request.ProjectID)
		if projectErr != nil {
			return outcome, errors.B.Op(op).Err(projectErr).Build()
		}
		result, invokeErr := tool.Invoke(ctx, alfred.ToolInvocation{Call: request.Call, ProjectRoot: project.Root})
		if invokeErr != nil {
			return outcome, errors.B.Op(op).Err(invokeErr).Build()
		}
		if (result.Result == nil) == (len(result.InputPrompt) == 0) {
			return outcome, errors.B.Op(op).KindInternal().Text("tool must return exactly one result or input prompt").Build()
		}
		outcome.Result, outcome.InputPrompt = result.Result, result.InputPrompt
		return outcome, nil
	case alfred.PolicyOutcomeDeny, alfred.PolicyOutcomeAsk:
		return outcome, nil
	default:
		return outcome, errors.B.
			Op(op).
			KindInternal().
			Textf("policy manager returned unknown outcome %q", outcome.Decision.Outcome).
			UserMsg("tool authorization failed").
			Build()
	}
}

func actionDigest(call alfred.ToolCall) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(call.ToolID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(call.Arguments)

	return hex.EncodeToString(hash.Sum(nil))
}

var _ alfred.Executor = (*Executor)(nil)
