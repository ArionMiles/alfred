package rpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type runReader interface {
	GetRun(context.Context, alfred.RunID) (alfred.Run, error)
	ListRuns(context.Context, alfred.SessionID, alfred.RunListOptions) (alfred.RunPage, error)
}

// RunHandler exposes run queries and controls over RPC.
type RunHandler struct {
	runs    runReader
	control alfred.RunControl
}

// NewRunHandler creates a run RPC handler.
func NewRunHandler(
	runs runReader,
	control alfred.RunControl,
) *RunHandler {
	return &RunHandler{
		runs:    runs,
		control: control,
	}
}

// GetRun returns a run by identity.
func (handler *RunHandler) GetRun(
	ctx context.Context,
	request *alfredv1.GetRunRequest,
) (*alfredv1.GetRunResponse, error) {
	const op = "GetRun"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	run, err := handler.runs.GetRun(ctx, alfred.RunID(request.GetRunId()))
	if err != nil {
		return nil, connectError(err)
	}
	encoded, err := encodeRun(run)
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.GetRunResponse{Run: encoded}, nil
}

// ListRuns returns one creation-ordered page for a session.
func (handler *RunHandler) ListRuns(
	ctx context.Context,
	request *alfredv1.ListRunsRequest,
) (*alfredv1.ListRunsResponse, error) {
	const op = "ListRuns"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	if request.GetPageSize() == 0 {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("page size is zero").
			UserMsg("page size must be greater than zero").
			Build())
	}
	if request.GetPageSize() > maxPageSize {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("page size %d exceeds maximum %d", request.GetPageSize(), maxPageSize).
			UserMsg("page size must not exceed 100").
			Build())
	}
	after, err := decodeRunCursor(request.GetAfter())
	if err != nil {
		return nil, connectError(errors.B.Op(op).Err(err).Build())
	}
	page, err := handler.runs.ListRuns(
		ctx,
		alfred.SessionID(request.GetSessionId()),
		alfred.RunListOptions{Limit: request.GetPageSize(), After: after},
	)
	if err != nil {
		return nil, connectError(err)
	}
	runs := make([]*alfredv1.Run, 0, len(page.Runs))
	for _, run := range page.Runs {
		encoded, err := encodeRun(run)
		if err != nil {
			return nil, connectError(errors.B.Op(op).Textf("encode run %q", run.ID).Err(err).Build())
		}
		runs = append(runs, encoded)
	}
	return &alfredv1.ListRunsResponse{
		Runs: runs,
		Next: encodeRunCursor(page.Next),
	}, nil
}

// ResolveApproval records an approval decision and redispatches its run.
func (handler *RunHandler) ResolveApproval(
	ctx context.Context,
	request *alfredv1.ResolveApprovalRequest,
) (*alfredv1.ResolveApprovalResponse, error) {
	const op = "ResolveApproval"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	outcome, err := decodeApprovalOutcome(request.GetOutcome())
	if err != nil {
		return nil, connectError(errors.B.Op(op).Err(err).Build())
	}
	if err := handler.control.ResolveApproval(
		ctx,
		alfred.RunID(request.GetRunId()),
		alfred.ApprovalResolution{
			ApprovalID: alfred.ApprovalID(request.GetApprovalId()),
			Outcome:    outcome,
			Reason:     request.GetReason(),
		},
	); err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.ResolveApprovalResponse{}, nil
}

// ProvideInput records requested input and redispatches its run.
func (handler *RunHandler) ProvideInput(
	ctx context.Context,
	request *alfredv1.ProvideInputRequest,
) (*alfredv1.ProvideInputResponse, error) {
	const op = "ProvideInput"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	content, err := decodeContentParts(request.GetContent())
	if err != nil {
		return nil, connectError(errors.B.Op(op).Err(err).Build())
	}
	if err := handler.control.ProvideInput(
		ctx,
		alfred.RunID(request.GetRunId()),
		alfred.InputResponse{
			RequestID: alfred.InputRequestID(request.GetRequestId()),
			Content:   content,
		},
	); err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.ProvideInputResponse{}, nil
}

// ResumeRun redispatches an interrupted run.
func (handler *RunHandler) ResumeRun(
	ctx context.Context,
	request *alfredv1.ResumeRunRequest,
) (*alfredv1.ResumeRunResponse, error) {
	const op = "ResumeRun"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	if err := handler.control.ResumeRun(ctx, alfred.RunID(request.GetRunId())); err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.ResumeRunResponse{}, nil
}

// CancelRun durably cancels a nonterminal run.
func (handler *RunHandler) CancelRun(
	ctx context.Context,
	request *alfredv1.CancelRunRequest,
) (*alfredv1.CancelRunResponse, error) {
	const op = "CancelRun"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	if err := handler.control.CancelRun(ctx, alfred.RunID(request.GetRunId())); err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.CancelRunResponse{}, nil
}

func decodeApprovalOutcome(outcome alfredv1.ApprovalOutcome) (alfred.ApprovalOutcome, error) {
	const op = "decodeApprovalOutcome"

	switch outcome {
	case alfredv1.ApprovalOutcome_APPROVAL_OUTCOME_APPROVED:
		return alfred.ApprovalOutcomeApproved, nil
	case alfredv1.ApprovalOutcome_APPROVAL_OUTCOME_DENIED:
		return alfred.ApprovalOutcomeDenied, nil
	default:
		return "", errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("unknown approval outcome %d", outcome).
			Build()
	}
}

func decodeRunCursor(cursor *alfredv1.RunCursor) (*alfred.RunCursor, error) {
	const op = "decodeRunCursor"

	if cursor == nil {
		return nil, nil
	}
	if err := cursor.GetCreatedAt().CheckValid(); err != nil {
		return nil, errors.B.
			Op(op).
			KindInvalidArgument().
			Text("cursor has invalid creation time").
			Err(err).
			Build()
	}
	return &alfred.RunCursor{
		CreatedAt: cursor.GetCreatedAt().AsTime(),
		ID:        alfred.RunID(cursor.GetId()),
	}, nil
}

func encodeRunCursor(cursor *alfred.RunCursor) *alfredv1.RunCursor {
	if cursor == nil {
		return nil
	}
	return &alfredv1.RunCursor{
		CreatedAt: timestamppb.New(cursor.CreatedAt),
		Id:        string(cursor.ID),
	}
}

var _ alfredv1connect.RunServiceHandler = (*RunHandler)(nil)
