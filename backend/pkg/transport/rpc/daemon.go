package rpc

import (
	"context"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/internal/buildinfo"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// DaemonHandler reports the identity and lifecycle state of the RPC service.
type DaemonHandler struct {
	instanceID string
	startedAt  time.Time
	state      atomic.Int32
}

// NewDaemonHandler creates a status handler in the starting state.
func NewDaemonHandler(instanceID string, startedAt time.Time) *DaemonHandler {
	handler := &DaemonHandler{
		instanceID: instanceID,
		startedAt:  startedAt,
	}
	handler.state.Store(int32(alfredv1.DaemonState_DAEMON_STATE_STARTING))
	return handler
}

// MarkReady reports that startup checks passed and application work may begin.
func (handler *DaemonHandler) MarkReady() {
	handler.state.Store(int32(alfredv1.DaemonState_DAEMON_STATE_READY))
}

// MarkStopping removes the service from readiness before shutdown begins.
func (handler *DaemonHandler) MarkStopping() {
	handler.state.Store(int32(alfredv1.DaemonState_DAEMON_STATE_STOPPING))
}

// GetStatus proves RPC liveness and reports current readiness.
func (handler *DaemonHandler) GetStatus(
	_ context.Context,
	request *alfredv1.GetStatusRequest,
) (*alfredv1.GetStatusResponse, error) {
	const op = "GetStatus"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	return &alfredv1.GetStatusResponse{
		InstanceId: handler.instanceID,
		Version:    buildinfo.Version,
		State:      alfredv1.DaemonState(handler.state.Load()),
		StartedAt:  timestamppb.New(handler.startedAt),
	}, nil
}

var _ alfredv1connect.DaemonServiceHandler = (*DaemonHandler)(nil)
