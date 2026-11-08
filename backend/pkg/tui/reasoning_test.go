package tui

import (
	"strings"
	"testing"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

func TestReasoningStreamingCompletionAndReplay(t *testing.T) {
	t.Parallel()
	model := readyModel(t)
	model.transcript.setSize(80, 30)
	for _, text := range []string{"Inspect ", "the file"} {
		model.transcript.addUpdate(reasoningUpdate(text))
	}
	model.transcript.addUpdate(&alfredv1.RunUpdate{Payload: &alfredv1.RunUpdate_ContentDelta{ContentDelta: &alfredv1.RunContentDelta{
		ModelCallId: "call", Delta: &alfredv1.ContentPart{Value: &alfredv1.ContentPart_Text{Text: "Provisional answer"}},
	}}})
	view := model.transcript.View(80, 30)
	if !strings.Contains(view, "Reasoning summary") || !strings.Contains(view, "Inspect the file") || !strings.Contains(view, "Provisional answer") {
		t.Fatalf("stream = %q", view)
	}
	completion := &alfredv1.WatchSessionResponse{Item: &alfredv1.WatchSessionResponse_Event{Event: &alfredv1.RunEvent{
		Sequence: 10, Payload: &alfredv1.RunEvent_ModelOutputCompleted{ModelOutputCompleted: &alfredv1.ModelOutputCompleted{
			ModelCallId: "call", Output: []*alfredv1.ModelOutputItem{
				{Payload: &alfredv1.ModelOutputItem_Reasoning{Reasoning: &alfredv1.Reasoning{Text: "Completed plan", TextKind: alfredv1.ReasoningTextKind_REASONING_TEXT_KIND_SUMMARY}}},
				{Payload: &alfredv1.ModelOutputItem_Message{Message: textMessage(alfredv1.MessageRole_MESSAGE_ROLE_ASSISTANT, "Completed answer")}},
				{Payload: &alfredv1.ModelOutputItem_Reasoning{Reasoning: &alfredv1.Reasoning{Text: "Final check", TextKind: alfredv1.ReasoningTextKind_REASONING_TEXT_KIND_REASONING}}},
			},
		}},
	}}}
	model.applySessionActivity(completion)
	model.applySessionActivity(completion)
	// Durable notifications can arrive before queued transient updates.
	model.transcript.addUpdate(reasoningUpdate("Late partial text"))
	view = model.transcript.View(80, 30)
	if strings.Contains(view, "Provisional") || strings.Contains(view, "Inspect the file") || strings.Count(view, "Completed answer") != 1 ||
		strings.Index(view, "Completed plan") > strings.Index(view, "Completed answer") || strings.Index(view, "Completed answer") > strings.Index(view, "Final check") {
		t.Fatalf("completed = %q", view)
	}
	replayed := readyModel(t)
	replayed.transcript.setSize(80, 30)
	replayed.applySessionActivity(completion)
	if replayed.transcript.View(80, 30) != view {
		t.Fatal("replay differs from completed live output")
	}
}

func TestReasoningProvisionalOutputIsDiscarded(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"gap", "disconnect", "cancel"} {
		t.Run(action, func(t *testing.T) {
			transcript := newTranscript()
			transcript.addUpdate(reasoningUpdate("Incomplete plan"))
			switch action {
			case "gap":
				transcript.markGap()
			case "disconnect":
				transcript.markDisconnected()
			case "cancel":
				transcript.discardProvisional()
			}
			if len(transcript.provisional) != 0 || strings.Contains(transcript.View(80, 6), "Incomplete plan") {
				t.Fatal("incomplete reasoning survived interruption")
			}
		})
	}
}

func TestCancelledRunIgnoresQueuedReasoning(t *testing.T) {
	t.Parallel()
	model := readyModel(t)
	model.activeRun = &alfredv1.Run{Id: "run", Status: alfredv1.RunStatus_RUN_STATUS_CANCELLED}
	update := reasoningUpdate("Late reasoning")
	update.RunId = "run"
	model.applySessionActivity(&alfredv1.WatchSessionResponse{Item: &alfredv1.WatchSessionResponse_Update{Update: update}})
	if len(model.transcript.provisional) != 0 {
		t.Fatal("queued reasoning reappeared after cancellation")
	}
}

func reasoningUpdate(text string) *alfredv1.RunUpdate {
	return &alfredv1.RunUpdate{Payload: &alfredv1.RunUpdate_ReasoningDelta{ReasoningDelta: &alfredv1.RunReasoningDelta{
		ModelCallId: "call", Text: text, TextKind: alfredv1.ReasoningTextKind_REASONING_TEXT_KIND_SUMMARY,
	}}}
}
