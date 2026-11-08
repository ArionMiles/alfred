package jsonl

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestEventCodecRoundTripsCurrentPayloads(t *testing.T) {
	t.Parallel()

	firstEvent := 25 * time.Millisecond
	firstOutput := 40 * time.Millisecond
	content := []alfred.ContentPart{
		alfred.TextContent{Text: "hello"},
		alfred.ResourceContent{
			Type: alfred.ContentKindImage,
			Resource: alfred.ResourceRef{
				URI: "file:///image.png", MediaType: "image/png", Name: "image.png",
			},
		},
	}
	call := alfred.ToolCall{
		ID: "tool-call-1", RunID: "run-1", ToolID: "read",
		Arguments: json.RawMessage(`{"path":"notes.md"}`),
	}
	payloads := []alfred.RunEventPayload{
		alfred.RunStateChanged{Status: alfred.RunStatusRunning},
		alfred.MessageAdded{Message: alfred.Message{Role: alfred.MessageRoleAssistant, Content: content}},
		alfred.ToolCallRequested{ModelCallID: "model-call-1", Call: call},
		alfred.ToolCallCompleted{Result: alfred.ToolResult{CallID: call.ID, Output: content, IsError: true}},
		alfred.PolicyEvaluated{
			CallID: call.ID,
			Decision: alfred.PolicyDecision{
				Outcome: alfred.PolicyOutcomeAsk, Reason: "approval required", RuleID: "rule-1",
			},
		},
		alfred.ApprovalRequested{Request: alfred.ApprovalRequest{
			ID: "approval-1", CallID: call.ID, ActionDigest: "digest", Reason: "review this",
		}},
		alfred.ApprovalResolved{Resolution: alfred.ApprovalResolution{
			ApprovalID: "approval-1", Outcome: alfred.ApprovalOutcomeApproved, Reason: "safe",
		}},
		alfred.InputRequested{Request: alfred.InputRequest{ID: "input-1", CallID: call.ID, Prompt: content}},
		alfred.InputReceived{Response: alfred.InputResponse{RequestID: "input-1", Content: content}},
		alfred.ModelCallRecorded{Metrics: alfred.ModelCallMetrics{
			CallID:  "model-call-1",
			Model:   alfred.ModelRef{ProviderID: "provider-1", ModelID: "model-1"},
			Outcome: alfred.ModelCallOutcomeCompleted,
			Usage: alfred.TokenUsage{
				InputTokens: 10, CachedInputTokens: 2, OutputTokens: 4, ReasoningTokens: 1,
				Additional: map[string]uint64{"accepted_prediction_tokens": 3},
			},
			Cost: alfred.ModelCost{
				Currency: "USD", InputMicros: 10, OutputMicros: 20, TotalMicros: 30,
				PricingID: "pricing-1", Estimated: true,
			},
			Timing: alfred.ModelCallTiming{
				DispatchedAt: time.Date(2026, time.October, 24, 10, 0, 0, 0, time.UTC),
				CompletedAt:  time.Date(2026, time.October, 24, 10, 0, 1, 0, time.UTC),
				Duration:     time.Second, TimeToFirstEvent: &firstEvent, TimeToFirstOutput: &firstOutput,
			},
		}},
		alfred.ModelCallCompleted{CallID: "model-call-1"},
		alfred.RunInterrupted{Reason: "usage_limit_reached", Message: "resume later"},
		alfred.ContextCompacted{
			FromEventID: "event-1", ThroughEventID: "event-9", Summary: content,
			ModelCallID: "model-call-2",
		},
	}

	for _, payload := range payloads {
		payload := payload
		t.Run(string(payload.Kind()), func(t *testing.T) {
			t.Parallel()

			want := alfred.RunEvent{
				ID: "event-10", RunID: "run-1", Sequence: 10,
				OccurredAt: time.Date(2026, time.October, 24, 10, 0, 2, 0, time.UTC),
				Visibility: alfred.RunEventVisibilityUser,
				Payload:    payload,
			}
			data, err := marshalEvent(want)
			if err != nil {
				t.Fatalf("marshalEvent() error = %v", err)
			}
			got, err := unmarshalEvent(data)
			if err != nil {
				t.Fatalf("unmarshalEvent() error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("event round trip = %#v, want %#v", got, want)
			}
		})
	}
}

func TestEventCodecRejectsUnknownSchemaVersion(t *testing.T) {
	t.Parallel()

	_, err := unmarshalEvent([]byte(`{"version":3,"kind":"run_state_changed","payload":{}}`))
	if got := errors.WhatKind(err); got != errors.InvalidInput {
		t.Fatalf("errors.WhatKind() = %#v, want %#v", got, errors.InvalidInput)
	}
}

func TestEventCodecRejectsUnknownEventKind(t *testing.T) {
	t.Parallel()

	_, err := unmarshalEvent([]byte(`{"version":1,"kind":"future_event","payload":{}}`))
	if got := errors.WhatKind(err); got != errors.InvalidInput {
		t.Fatalf("errors.WhatKind() = %#v, want %#v", got, errors.InvalidInput)
	}
}

func BenchmarkEventCodec(b *testing.B) {
	event := alfred.RunEvent{
		ID: "event-1", RunID: "run-1", Sequence: 1,
		OccurredAt: time.Date(2026, time.October, 24, 10, 0, 0, 0, time.UTC),
		Visibility: alfred.RunEventVisibilityUser,
		Payload: alfred.MessageAdded{Message: alfred.Message{
			Role: alfred.MessageRoleAssistant,
			Content: []alfred.ContentPart{
				alfred.TextContent{Text: "A representative completed assistant message."},
			},
		}},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		data, err := marshalEvent(event)
		if err != nil {
			b.Fatalf("marshalEvent() error = %v", err)
		}
		if _, err := unmarshalEvent(data); err != nil {
			b.Fatalf("unmarshalEvent() error = %v", err)
		}
	}
}
