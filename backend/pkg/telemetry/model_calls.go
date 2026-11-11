// Package telemetry provides instrumentation for Alfred's runtime boundaries.
package telemetry

import (
	"context"
	"iter"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// ModelCalls records metrics for every provider stream.
func ModelCalls(events alfred.RunEventSink) providers.Middleware {
	return func(provider alfred.ModelProvider) alfred.ModelProvider {
		return &modelCallProvider{
			next:   provider,
			events: events,
		}
	}
}

type modelCallProvider struct {
	next   alfred.ModelProvider
	events alfred.RunEventSink
}

func (provider *modelCallProvider) ID() alfred.ProviderID {
	return provider.next.ID()
}

func (provider *modelCallProvider) Models(ctx context.Context) ([]alfred.ModelInfo, error) {
	return provider.next.Models(ctx)
}

func (provider *modelCallProvider) ResolveModel(
	ctx context.Context,
	modelID alfred.ModelID,
) (alfred.ModelInfo, error) {
	return provider.next.ResolveModel(ctx, modelID)
}

//nolint:gocognit // Stream keeps event observation and exactly-once metric persistence in one iterator.
func (provider *modelCallProvider) Stream(
	ctx context.Context,
	request alfred.ModelRequest,
) iter.Seq2[alfred.ModelEvent, error] {
	return func(yield func(alfred.ModelEvent, error) bool) {
		const op = "Stream"

		dispatchedAt := time.Now()
		metrics := alfred.ModelCallMetrics{
			CallID: request.ID,
			Model:  request.Model,
			Timing: alfred.ModelCallTiming{DispatchedAt: dispatchedAt},
		}
		var completed bool

		for event, streamErr := range provider.next.Stream(ctx, request) {
			if streamErr != nil {
				metrics.Outcome = alfred.ModelCallOutcomeFailed
				if ctx.Err() != nil {
					metrics.Outcome = alfred.ModelCallOutcomeCancelled
				}
				finishModelCall(&metrics, time.Now())
				if telemetryErr := provider.record(context.WithoutCancel(ctx), request.RunID, metrics); telemetryErr != nil {
					streamErr = errors.B.
						Op(op).
						Textf("record failed model call %q", request.ID).
						Err(streamErr).
						Err(telemetryErr).
						Build()
				}
				yield(event, streamErr)
				return
			}

			eventAt := time.Now()
			observeModelEvent(&metrics, event, dispatchedAt, eventAt)
			if _, ok := event.Payload.(alfred.ModelCompleted); ok {
				completed = true
			}
			if !yield(event, nil) {
				metrics.Outcome = alfred.ModelCallOutcomeFailed
				finishModelCall(&metrics, time.Now())
				_ = provider.record(context.WithoutCancel(ctx), request.RunID, metrics)
				return
			}
		}

		metrics.Outcome = alfred.ModelCallOutcomeFailed
		if completed {
			metrics.Outcome = alfred.ModelCallOutcomeCompleted
		}
		finishModelCall(&metrics, time.Now())
		if err := provider.record(context.WithoutCancel(ctx), request.RunID, metrics); err != nil {
			yield(alfred.ModelEvent{}, errors.B.
				Op(op).
				Textf("record model call %q", request.ID).
				Err(err).
				Build())
		}
	}
}

func observeModelEvent(
	metrics *alfred.ModelCallMetrics,
	event alfred.ModelEvent,
	dispatchedAt time.Time,
	eventAt time.Time,
) {
	if metrics.Timing.TimeToFirstEvent == nil {
		duration := eventAt.Sub(dispatchedAt)
		metrics.Timing.TimeToFirstEvent = &duration
	}

	switch payload := event.Payload.(type) {
	case alfred.ModelContentDelta, alfred.ModelMessage, alfred.ModelToolCall:
		if metrics.Timing.TimeToFirstOutput == nil {
			duration := eventAt.Sub(dispatchedAt)
			metrics.Timing.TimeToFirstOutput = &duration
		}
	case alfred.ModelUsage:
		metrics.Usage = payload.Tokens
	}
}

func finishModelCall(metrics *alfred.ModelCallMetrics, completedAt time.Time) {
	metrics.Timing.CompletedAt = completedAt
	metrics.Timing.Duration = completedAt.Sub(metrics.Timing.DispatchedAt)
}

func (provider *modelCallProvider) record(
	ctx context.Context,
	runID alfred.RunID,
	metrics alfred.ModelCallMetrics,
) error {
	const op = "record"

	_, err := provider.events.Append(ctx, alfred.NewRunEvent{
		RunID:      runID,
		Visibility: alfred.RunEventVisibilityInternal,
		Payload:    alfred.ModelCallRecorded{Metrics: metrics},
	})
	if err != nil {
		return errors.B.
			Op(op).
			Textf("append metrics for model call %q", metrics.CallID).
			Err(err).
			Build()
	}
	return nil
}

var _ alfred.ModelProvider = (*modelCallProvider)(nil)
