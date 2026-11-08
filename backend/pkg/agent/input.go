package agent

import (
	"context"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func (runner *Runner) requestToolInput(ctx context.Context, runID alfred.RunID, callID alfred.ToolCallID, prompt []alfred.ContentPart) error {
	const op = "requestToolInput"
	// One input request belongs to one immutable tool call; resumption replays its response.
	_, err := runner.events.Append(ctx, alfred.NewRunEvent{
		RunID: runID, Visibility: alfred.RunEventVisibilityUser,
		Payload: alfred.InputRequested{Request: alfred.InputRequest{ID: alfred.InputRequestID(callID), CallID: callID, Prompt: prompt}},
	})
	if err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	return runner.transitionRun(ctx, runID, alfred.RunStatusRunning, alfred.RunStatusWaitingForInput)
}
