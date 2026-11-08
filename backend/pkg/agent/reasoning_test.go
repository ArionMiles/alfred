package agent

import (
	"testing"

	"github.com/ArionMiles/alfred"
)

func TestRunnerBroadcastsReadableReasoningAndCommitsOnlyCompletedOutput(t *testing.T) {
	t.Parallel()
	for _, complete := range []bool{false, true} {
		name := "incomplete"
		if complete {
			name = "complete"
		}
		t.Run(name, func(t *testing.T) {
			model := testModel()
			fixture := newRunnerFixture(t, testRun(model.Ref), model)
			fixture.provider.events = []alfred.ModelEvent{{Payload: alfred.ModelReasoningDelta{
				Text: "Plan", TextKind: alfred.ReasoningTextSummary,
			}}}
			if complete {
				fixture.provider.events = append(fixture.provider.events, alfred.ModelEvent{
					Payload: alfred.ModelReasoning{Reasoning: alfred.Reasoning{Text: "Plan", TextKind: alfred.ReasoningTextSummary}},
					Native:  alfred.NativeData{"private.v1": []byte(`{"signature":"secret"}`)},
				})
			}
			fixture.provider.events = append(fixture.provider.events, alfred.ModelEvent{Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonStop}})
			err := fixture.runner().Execute(t.Context(), testRun(model.Ref).ID)
			if (err == nil) != complete {
				t.Fatalf("complete=%v error=%v", complete, err)
			}
			updates := fixture.updates.snapshot()
			if len(updates) != 1 {
				t.Fatalf("updates=%v", updates)
			}
			delta, ok := updates[0].Payload.(alfred.RunReasoningDelta)
			if !ok || delta.Delta.Text != "Plan" || updates[0].Visibility != alfred.RunEventVisibilityUser {
				t.Fatalf("delta=%#v", updates[0])
			}
			committed := false
			for _, event := range fixture.events.snapshot() {
				if output, ok := event.Payload.(alfred.ModelCallCompleted); ok {
					committed = true
					if len(output.Output) != 1 || output.Output[0].Native["private.v1"] == nil {
						t.Fatal("checkpoint lost native reasoning")
					}
				}
			}
			if committed != complete {
				t.Fatal("incomplete reasoning was committed")
			}
		})
	}
}
