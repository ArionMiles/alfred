package tui

import (
	tea "charm.land/bubbletea/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type runResumedMsg struct {
	runID string
	err   error
}

func (model Model) resumeRun() (tea.Model, tea.Cmd) {
	const op = "resumeRun"

	switch {
	case model.replaying || model.submitting || model.canceling || model.resuming || model.resolvingGate != "" || model.settingTrust:
		model.actionErr = errors.B.Op(op).KindConflict().UserMsg("Wait for the current action to finish before resuming.").Build()
	case model.activeRun.GetStatus() != alfredv1.RunStatus_RUN_STATUS_INTERRUPTED:
		model.actionErr = errors.B.Op(op).KindConflict().UserMsg("There is no interrupted run to resume in this session.").Build()
	default:
		model.resuming = true
		model.actionErr = nil
		model.resizeTranscript()
		return model, tea.Batch(model.requestResume(), model.progress.Tick)
	}
	model.resizeTranscript()
	return model, model.focusInput()
}

func (model Model) requestResume() tea.Cmd {
	runID := model.activeRun.GetId()
	return func() tea.Msg {
		_, err := model.clients.Run.ResumeRun(model.ctx, &alfredv1.ResumeRunRequest{RunId: runID})
		return runResumedMsg{runID: runID, err: err}
	}
}

func (model Model) updateRunResumed(message runResumedMsg) (tea.Model, tea.Cmd) {
	if model.activeRun.GetId() != message.runID {
		return model, nil
	}
	model.resuming = false
	model.actionErr = message.err
	// Run status comes from the watch, which may have advanced before this reply.
	model.resizeTranscript()
	return model, model.focusInput()
}
