package telemetry

import (
	"context"
	"iter"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/providers"
)

func TestModelCallsRecordsProviderStreamMetrics(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		request := alfred.ModelRequest{
			ID:    "call-1",
			RunID: "run-1",
			Model: alfred.ModelRef{ProviderID: "provider-1", ModelID: "model-1"},
		}
		usage := alfred.TokenUsage{InputTokens: 12, OutputTokens: 3}
		controller := gomock.NewController(t)
		var recorded alfred.NewRunEvent
		sink := mocks.NewMockRunEventSink(controller)
		sink.EXPECT().Append(gomock.Any(), gomock.Any()).DoAndReturn(func(
			_ context.Context,
			event alfred.NewRunEvent,
		) (alfred.RunEvent, error) {
			recorded = event
			return alfred.RunEvent{RunID: event.RunID, Payload: event.Payload}, nil
		})
		next := mocks.NewMockModelProvider(controller)
		next.EXPECT().ID().Return(request.Model.ProviderID)
		next.EXPECT().Stream(gomock.Any(), request).Return(eventSequence([]alfred.ModelEvent{
			{Payload: alfred.ModelContentDelta{Delta: alfred.TextContent{Text: "Hello"}}},
			{Payload: alfred.ModelUsage{Tokens: usage}},
			{Payload: alfred.ModelCompleted{Reason: alfred.ModelFinishReasonStop}},
		}, nil, time.Second))
		registry, err := providers.NewRegistry(
			[]alfred.ModelProvider{next},
			providers.WithMiddleware(ModelCalls(sink)),
		)
		if err != nil {
			t.Fatalf("NewRegistry() error = %v", err)
		}
		provider, err := registry.ResolveProvider(t.Context(), request.Model.ProviderID)
		if err != nil {
			t.Fatalf("ResolveProvider() error = %v", err)
		}

		for _, streamErr := range provider.Stream(t.Context(), request) {
			if streamErr != nil {
				t.Fatalf("Stream() error = %v", streamErr)
			}
		}

		metrics := recorded.Payload.(alfred.ModelCallRecorded).Metrics
		if metrics.CallID != request.ID || metrics.Model != request.Model {
			t.Fatalf("model call identity = %#v, want request identity", metrics)
		}
		if metrics.Outcome != alfred.ModelCallOutcomeCompleted {
			t.Fatalf("outcome = %q, want %q", metrics.Outcome, alfred.ModelCallOutcomeCompleted)
		}
		if metrics.Usage.InputTokens != usage.InputTokens ||
			metrics.Usage.OutputTokens != usage.OutputTokens {
			t.Fatalf("usage = %#v, want %#v", metrics.Usage, usage)
		}
		if metrics.Timing.Duration != 4*time.Second {
			t.Fatalf("duration = %v, want 4s", metrics.Timing.Duration)
		}
		if metrics.Timing.TimeToFirstEvent == nil ||
			*metrics.Timing.TimeToFirstEvent != time.Second {
			t.Fatalf("time to first event = %v, want 1s", metrics.Timing.TimeToFirstEvent)
		}
		if metrics.Timing.TimeToFirstOutput == nil ||
			*metrics.Timing.TimeToFirstOutput != time.Second {
			t.Fatalf("time to first output = %v, want 1s", metrics.Timing.TimeToFirstOutput)
		}
	})
}

func TestModelCallsRecordsCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		controller := gomock.NewController(t)
		var recorded alfred.NewRunEvent
		sink := mocks.NewMockRunEventSink(controller)
		sink.EXPECT().Append(gomock.Any(), gomock.Any()).DoAndReturn(func(
			_ context.Context,
			event alfred.NewRunEvent,
		) (alfred.RunEvent, error) {
			recorded = event
			return alfred.RunEvent{RunID: event.RunID, Payload: event.Payload}, nil
		})
		next := mocks.NewMockModelProvider(controller)
		next.EXPECT().Stream(gomock.Any(), gomock.Any()).Return(eventSequence(nil, ctx.Err(), time.Second))
		provider := ModelCalls(sink)(next)

		var streamErr error
		for _, err := range provider.Stream(ctx, alfred.ModelRequest{ID: "call-1", RunID: "run-1"}) {
			streamErr = err
		}
		if streamErr == nil {
			t.Fatal("Stream() error = nil, want cancellation")
		}

		metrics := recorded.Payload.(alfred.ModelCallRecorded).Metrics
		if metrics.Outcome != alfred.ModelCallOutcomeCancelled {
			t.Fatalf("outcome = %q, want %q", metrics.Outcome, alfred.ModelCallOutcomeCancelled)
		}
	})
}

func eventSequence(events []alfred.ModelEvent, streamErr error, delay time.Duration) iter.Seq2[alfred.ModelEvent, error] {
	return func(yield func(alfred.ModelEvent, error) bool) {
		for _, event := range events {
			time.Sleep(delay)
			if !yield(event, nil) {
				return
			}
		}
		time.Sleep(delay)
		if streamErr != nil {
			yield(alfred.ModelEvent{}, streamErr)
		}
	}
}
