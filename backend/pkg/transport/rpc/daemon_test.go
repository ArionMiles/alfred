package rpc

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

func TestDaemonHandlerReportsLifecycle(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.October, 26, 18, 0, 0, 0, time.UTC)
	handler := NewDaemonHandler("instance-1", startedAt)

	assertDaemonState(t, handler, alfredv1.DaemonState_DAEMON_STATE_STARTING, startedAt)
	handler.MarkReady()
	assertDaemonState(t, handler, alfredv1.DaemonState_DAEMON_STATE_READY, startedAt)
	handler.MarkStopping()
	assertDaemonState(t, handler, alfredv1.DaemonState_DAEMON_STATE_STOPPING, startedAt)
}

func TestDaemonHandlerRejectsNilRequest(t *testing.T) {
	t.Parallel()

	handler := NewDaemonHandler("instance-1", time.Now())
	if _, err := handler.GetStatus(t.Context(), nil); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("GetStatus() code = %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
	}
}

func assertDaemonState(
	t *testing.T,
	handler *DaemonHandler,
	want alfredv1.DaemonState,
	startedAt time.Time,
) {
	t.Helper()
	response, err := handler.GetStatus(t.Context(), &alfredv1.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if response.GetInstanceId() != "instance-1" || response.GetVersion() != "dev" ||
		response.GetState() != want || !response.GetStartedAt().AsTime().Equal(startedAt) {
		t.Fatalf("GetStatus() = %v, started_at want %v", response, timestamppb.New(startedAt))
	}
}
