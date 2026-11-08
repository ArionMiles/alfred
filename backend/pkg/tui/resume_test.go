package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestResumeThroughSlashAliasAndPalette(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{"/resume", "/continue", "palette"} {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			control := mocks.NewMockRunControl(gomock.NewController(t))
			control.EXPECT().ResumeRun(gomock.Any(), alfred.RunID("run-1")).Return(nil)
			server := connect.NewServer()
			alfredv1connect.RegisterRunServiceHandler(server, rpc.NewRunHandler(nil, control))
			model := readyModel(t)
			model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
			model.sessionID = "session-1"
			model.activeRun = &alfredv1.Run{Id: "run-1", Status: alfredv1.RunStatus_RUN_STATUS_INTERRUPTED}
			model.transcript.addMessage(textMessage(alfredv1.MessageRole_MESSAGE_ROLE_USER, "original message"))
			model.gateInput.SetValue("response draft")
			if entry == "palette" {
				model.composer.SetValue("message draft")
				opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				model = opened.(Model)
				model.palette.Select(4)
			} else {
				model.composer.SetValue(entry)
			}
			updated, request := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = updated.(Model)
			if !model.resuming || model.canSubmit() || model.canCancelActiveRun() || !strings.Contains(model.progressView(), "Resuming") {
				t.Fatal("resume did not show progress and block conflicting run actions")
			}
			repeated, _ := model.resumeRun()
			if repeated.(Model).actionErr == nil {
				t.Fatal("duplicate resume was accepted")
			}
			newSession, _ := model.newSession()
			if newSession.(Model).sessionID != "session-1" {
				t.Fatal("new session was allowed while resuming")
			}
			// Watch events can arrive before the RPC acknowledgement.
			model.applySessionActivity(&alfredv1.WatchSessionResponse{
				Item: &alfredv1.WatchSessionResponse_Event{Event: &alfredv1.RunEvent{
					RunId: "run-1", Sequence: 1,
					Payload: &alfredv1.RunEvent_RunStateChanged{RunStateChanged: &alfredv1.RunStateChanged{
						Status: alfredv1.RunStatus_RUN_STATUS_COMPLETED,
					}},
				}},
			})
			completed, _ := model.Update(batchMessage[runResumedMsg](t, request))
			model = completed.(Model)
			if model.resuming || model.actionErr != nil || model.activeRun.GetStatus() != alfredv1.RunStatus_RUN_STATUS_COMPLETED ||
				model.sessionID != "session-1" || len(model.transcript.entries) != 1 || model.gateInput.Value() != "response draft" {
				t.Fatal("resume acknowledgement changed conversation or overwrote watched status")
			}
			if entry == "palette" && model.composer.Value() != "message draft" {
				t.Fatal("resume cleared the message draft")
			}
		})
	}
}

func TestFailedResumeKeepsInterruptedRunAndDraft(t *testing.T) {
	const op = "TestFailedResumeKeepsInterruptedRunAndDraft"

	t.Parallel()
	control := mocks.NewMockRunControl(gomock.NewController(t))
	control.EXPECT().ResumeRun(gomock.Any(), alfred.RunID("run-1")).Return(
		errors.B.Op(op).KindUnavailable().UserMsg("Could not resume the run.").Build(),
	)
	server := connect.NewServer()
	alfredv1connect.RegisterRunServiceHandler(server, rpc.NewRunHandler(nil, control))
	model := readyModel(t)
	model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
	model.activeRun = &alfredv1.Run{Id: "run-1", Status: alfredv1.RunStatus_RUN_STATUS_INTERRUPTED}
	model.composer.SetValue("draft")
	started, request := model.resumeRun()
	completed, _ := started.(Model).Update(batchMessage[runResumedMsg](t, request))
	model = completed.(Model)
	if model.resuming || model.actionErr == nil || model.activeRun.GetStatus() != alfredv1.RunStatus_RUN_STATUS_INTERRUPTED ||
		model.composer.Value() != "draft" || !model.composer.Focused() {
		t.Fatal("failed resume changed the interrupted run or draft")
	}
}

func TestResumeRequiresInterruptedRunAndCompletedReplay(t *testing.T) {
	t.Parallel()
	model := readyModel(t)
	for _, run := range []*alfredv1.Run{
		nil,
		{Id: "run-1", Status: alfredv1.RunStatus_RUN_STATUS_FAILED},
		{Id: "run-1", Status: alfredv1.RunStatus_RUN_STATUS_RUNNING},
	} {
		model.activeRun = run
		updated, _ := model.resumeRun()
		if updated.(Model).resuming || updated.(Model).actionErr == nil {
			t.Fatal("resume accepted a run that was not interrupted")
		}
	}
	model.activeRun = &alfredv1.Run{Id: "run-1", Status: alfredv1.RunStatus_RUN_STATUS_INTERRUPTED}
	model.replaying = true
	updated, _ := model.resumeRun()
	if updated.(Model).resuming || updated.(Model).actionErr == nil {
		t.Fatal("resume accepted an incomplete replay")
	}
}
