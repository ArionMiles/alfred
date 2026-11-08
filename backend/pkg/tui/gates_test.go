package tui

import (
	"strings"
	"testing"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

func TestPendingGatesRebuildFromDurableEvents(t *testing.T) {
	t.Parallel()

	var gates pendingGates
	gates.apply(approvalRequestedEvent(1, "run-1", "approval-1", "Allow the command?"))
	gates.apply(inputRequestedEvent(2, "run-2", "input-1", "Which environment?"))

	current, ok := gates.current()
	if !ok || current.key() != "input:input-1" {
		t.Fatalf("current gate = %+v, %v", current, ok)
	}
	gates.apply(&alfredv1.RunEvent{
		RunId: "run-2", Sequence: 3,
		Payload: &alfredv1.RunEvent_InputReceived{InputReceived: &alfredv1.InputReceived{
			Response: &alfredv1.RunInputResponse{RequestId: "input-1"},
		}},
	})
	current, ok = gates.current()
	if !ok || current.key() != "approval:approval-1" {
		t.Fatalf("remaining gate = %+v, %v", current, ok)
	}
	gates.apply(&alfredv1.RunEvent{
		RunId: "run-1", Sequence: 4,
		Payload: &alfredv1.RunEvent_RunStateChanged{RunStateChanged: &alfredv1.RunStateChanged{
			Status: alfredv1.RunStatus_RUN_STATUS_CANCELLED,
		}},
	})
	if _, ok := gates.current(); ok {
		t.Fatal("terminal run left a pending interaction gate")
	}
}

func TestApprovalGateRendersInline(t *testing.T) {
	t.Parallel()

	model := readyModel(t)
	model.applySessionActivity(&alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_Event{Event: approvalRequestedEvent(
			1,
			"run-1",
			"approval-1",
			"Allow reading the credentials file?",
		)},
	})

	view := model.View().Content
	if !strings.Contains(view, "Approval required") ||
		!strings.Contains(view, "Allow reading the credentials file?") ||
		!strings.Contains(view, "[y] Approve  [n] Deny") {
		t.Fatalf("approval gate view = %q", view)
	}
}

func approvalRequestedEvent(
	sequence uint64,
	runID, approvalID, reason string,
) *alfredv1.RunEvent {
	return &alfredv1.RunEvent{
		RunId: runID, Sequence: sequence,
		Payload: &alfredv1.RunEvent_ApprovalRequested{ApprovalRequested: &alfredv1.ApprovalRequested{
			Request: &alfredv1.ApprovalRequest{Id: approvalID, Reason: reason},
		}},
	}
}

func inputRequestedEvent(
	sequence uint64,
	runID, requestID, prompt string,
) *alfredv1.RunEvent {
	return &alfredv1.RunEvent{
		RunId: runID, Sequence: sequence,
		Payload: &alfredv1.RunEvent_InputRequested{InputRequested: &alfredv1.InputRequested{
			Request: &alfredv1.RunInputRequest{
				Id: requestID,
				Prompt: []*alfredv1.ContentPart{{
					Value: &alfredv1.ContentPart_Text{Text: prompt},
				}},
			},
		}},
	}
}
