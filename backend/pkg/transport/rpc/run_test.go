package rpc

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/mocks"
)

func TestRunHandlerGetsRun(t *testing.T) {
	t.Parallel()

	run := testRun()
	runs := mocks.NewMockRunStore(gomock.NewController(t))
	runs.EXPECT().GetRun(gomock.Any(), run.ID).Return(run, nil)
	handler := NewRunHandler(runs, mocks.NewMockRunControl(gomock.NewController(t)))

	response, err := handler.GetRun(t.Context(), &alfredv1.GetRunRequest{RunId: string(run.ID)})
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if response.GetRun().GetId() != string(run.ID) ||
		response.GetRun().GetStatus() != alfredv1.RunStatus_RUN_STATUS_RUNNING {
		t.Fatalf("GetRun() = %v", response)
	}
}

func TestRunHandlerListsRunsWithCursor(t *testing.T) {
	t.Parallel()

	run := testRun()
	after := alfred.RunCursor{CreatedAt: run.CreatedAt.Add(-time.Hour), ID: "run-0"}
	next := alfred.RunCursor{CreatedAt: run.CreatedAt, ID: run.ID}
	runs := mocks.NewMockRunStore(gomock.NewController(t))
	runs.EXPECT().ListRuns(
		gomock.Any(),
		run.SessionID,
		alfred.RunListOptions{Limit: 10, After: &after},
	).Return(alfred.RunPage{Runs: []alfred.Run{run}, Next: &next}, nil)
	handler := NewRunHandler(runs, mocks.NewMockRunControl(gomock.NewController(t)))

	response, err := handler.ListRuns(t.Context(), &alfredv1.ListRunsRequest{
		SessionId: string(run.SessionID),
		PageSize:  10,
		After: &alfredv1.RunCursor{
			CreatedAt: timestamppb.New(after.CreatedAt),
			Id:        string(after.ID),
		},
	})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	wantNext := &alfredv1.RunCursor{
		CreatedAt: timestamppb.New(next.CreatedAt),
		Id:        string(next.ID),
	}
	if len(response.GetRuns()) != 1 || response.GetRuns()[0].GetId() != string(run.ID) ||
		!proto.Equal(response.GetNext(), wantNext) {
		t.Fatalf("ListRuns() = %v", response)
	}
}

func TestRunHandlerMapsControlRequests(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	control := mocks.NewMockRunControl(controller)
	handler := NewRunHandler(mocks.NewMockRunStore(controller), control)
	runID := alfred.RunID("run-1")
	control.EXPECT().ResolveApproval(gomock.Any(), runID, alfred.ApprovalResolution{
		ApprovalID: "approval-1",
		Outcome:    alfred.ApprovalOutcomeApproved,
		Reason:     "approved for this run",
	}).Return(nil)
	control.EXPECT().ProvideInput(gomock.Any(), runID, alfred.InputResponse{
		RequestID: "input-1",
		Content:   []alfred.ContentPart{alfred.TextContent{Text: "Friday"}},
	}).Return(nil)
	control.EXPECT().ResumeRun(gomock.Any(), runID).Return(nil)
	control.EXPECT().CancelRun(gomock.Any(), runID).Return(nil)

	if _, err := handler.ResolveApproval(t.Context(), &alfredv1.ResolveApprovalRequest{
		RunId:      string(runID),
		ApprovalId: "approval-1",
		Outcome:    alfredv1.ApprovalOutcome_APPROVAL_OUTCOME_APPROVED,
		Reason:     "approved for this run",
	}); err != nil {
		t.Fatalf("ResolveApproval() error = %v", err)
	}
	if _, err := handler.ProvideInput(t.Context(), &alfredv1.ProvideInputRequest{
		RunId:     string(runID),
		RequestId: "input-1",
		Content: []*alfredv1.ContentPart{{
			Value: &alfredv1.ContentPart_Text{Text: "Friday"},
		}},
	}); err != nil {
		t.Fatalf("ProvideInput() error = %v", err)
	}
	if _, err := handler.ResumeRun(t.Context(), &alfredv1.ResumeRunRequest{
		RunId: string(runID),
	}); err != nil {
		t.Fatalf("ResumeRun() error = %v", err)
	}
	if _, err := handler.CancelRun(t.Context(), &alfredv1.CancelRunRequest{
		RunId: string(runID),
	}); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
}

func TestRunHandlerRejectsInvalidListAndApprovalRequests(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	handler := NewRunHandler(
		mocks.NewMockRunStore(controller),
		mocks.NewMockRunControl(controller),
	)

	if _, err := handler.ListRuns(t.Context(), &alfredv1.ListRunsRequest{}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("ListRuns() code = %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
	}
	if _, err := handler.ResolveApproval(
		t.Context(),
		&alfredv1.ResolveApprovalRequest{},
	); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("ResolveApproval() code = %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
	}
	if _, err := handler.ListRuns(t.Context(), &alfredv1.ListRunsRequest{
		PageSize: maxPageSize + 1,
	}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("oversized ListRuns() code = %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
	}
}

func testRun() alfred.Run {
	return alfred.Run{
		ID:           "run-1",
		InvocationID: "invocation-1",
		SessionID:    "session-1",
		DefaultModel: alfred.ModelRef{ProviderID: "provider-1", ModelID: "model-1"},
		Status:       alfred.RunStatusRunning,
		CreatedAt:    time.Date(2026, time.October, 26, 16, 0, 0, 0, time.UTC),
	}
}
