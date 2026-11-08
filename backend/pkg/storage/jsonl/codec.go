// Package jsonl persists Alfred's durable session event history as JSONL.
package jsonl

import (
	"encoding/json"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// schemaVersion identifies the durable event-record format. Changing it requires
// an explicit decoding strategy for histories written with earlier versions.
const schemaVersion = 2

type eventRecord struct {
	Version    uint32                    `json:"version"`
	ID         string                    `json:"id"`
	RunID      string                    `json:"run_id"`
	Sequence   uint64                    `json:"sequence"`
	OccurredAt time.Time                 `json:"occurred_at"`
	Visibility alfred.RunEventVisibility `json:"visibility"`
	Kind       alfred.RunEventKind       `json:"kind"`
	Payload    json.RawMessage           `json:"payload"`
}

type contentRecord struct {
	Kind     alfred.ContentKind `json:"kind"`
	Text     string             `json:"text,omitempty"`
	Resource *resourceRecord    `json:"resource,omitempty"`
}

type resourceRecord struct {
	URI       string `json:"uri"`
	MediaType string `json:"media_type,omitempty"`
	Name      string `json:"name,omitempty"`
}

type messageRecord struct {
	Role    alfred.MessageRole `json:"role"`
	Content []contentRecord    `json:"content"`
}

type toolCallRecord struct {
	ID        string          `json:"id"`
	RunID     string          `json:"run_id"`
	ToolID    string          `json:"tool_id"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResultRecord struct {
	CallID  string          `json:"call_id"`
	Output  []contentRecord `json:"output"`
	IsError bool            `json:"is_error"`
}

type policyDecisionRecord struct {
	Outcome alfred.PolicyOutcome `json:"outcome"`
	Reason  string               `json:"reason,omitempty"`
	RuleID  string               `json:"rule_id,omitempty"`
}

type approvalRequestRecord struct {
	ID           string `json:"id"`
	CallID       string `json:"call_id"`
	ActionDigest string `json:"action_digest"`
	Reason       string `json:"reason,omitempty"`
}

type approvalResolutionRecord struct {
	ApprovalID string                 `json:"approval_id"`
	Outcome    alfred.ApprovalOutcome `json:"outcome"`
	Reason     string                 `json:"reason,omitempty"`
}

type inputRequestRecord struct {
	ID     string          `json:"id"`
	CallID string          `json:"call_id,omitempty"`
	Prompt []contentRecord `json:"prompt"`
}

type inputResponseRecord struct {
	RequestID string          `json:"request_id"`
	Content   []contentRecord `json:"content"`
}

type modelRefRecord struct {
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
}

type tokenUsageRecord struct {
	InputTokens       uint64            `json:"input_tokens"`
	CachedInputTokens uint64            `json:"cached_input_tokens"`
	OutputTokens      uint64            `json:"output_tokens"`
	ReasoningTokens   uint64            `json:"reasoning_tokens"`
	Additional        map[string]uint64 `json:"additional,omitempty"`
}

type modelCostRecord struct {
	Currency     string `json:"currency,omitempty"`
	InputMicros  uint64 `json:"input_micros"`
	OutputMicros uint64 `json:"output_micros"`
	TotalMicros  uint64 `json:"total_micros"`
	PricingID    string `json:"pricing_id,omitempty"`
	Estimated    bool   `json:"estimated"`
}

type modelCallTimingRecord struct {
	DispatchedAt      time.Time      `json:"dispatched_at"`
	CompletedAt       time.Time      `json:"completed_at"`
	Duration          time.Duration  `json:"duration_nanos"`
	TimeToFirstEvent  *time.Duration `json:"time_to_first_event_nanos,omitempty"`
	TimeToFirstOutput *time.Duration `json:"time_to_first_output_nanos,omitempty"`
}

type modelCallMetricsRecord struct {
	CallID  string                  `json:"call_id"`
	Model   modelRefRecord          `json:"model"`
	Outcome alfred.ModelCallOutcome `json:"outcome"`
	Usage   tokenUsageRecord        `json:"usage"`
	Cost    modelCostRecord         `json:"cost"`
	Timing  modelCallTimingRecord   `json:"timing"`
}

func marshalEvent(event alfred.RunEvent) ([]byte, error) {
	const op = "marshalEvent"

	if event.Payload == nil {
		return nil, errors.B.Op(op).KindInvalidInput().Text("run event has no payload").Build()
	}
	payload, err := marshalPayload(event.Payload)
	if err != nil {
		return nil, errors.B.Op(op).Textf("marshal %q payload", event.Payload.Kind()).Err(err).Build()
	}
	record := eventRecord{
		Version:    schemaVersion,
		ID:         string(event.ID),
		RunID:      string(event.RunID),
		Sequence:   event.Sequence,
		OccurredAt: event.OccurredAt,
		Visibility: event.Visibility,
		Kind:       event.Payload.Kind(),
		Payload:    payload,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, errors.B.Op(op).Text("marshal event record").Err(err).Build()
	}
	return data, nil
}

func unmarshalEvent(data []byte) (alfred.RunEvent, error) {
	const op = "unmarshalEvent"

	var record eventRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return alfred.RunEvent{}, errors.B.
			Op(op).
			KindInvalidInput().
			Text("decode event record").
			Err(err).
			Build()
	}
	if record.Version != 1 && record.Version != schemaVersion {
		return alfred.RunEvent{}, errors.B.
			Op(op).
			KindInvalidInput().
			Textf("event record has schema version %d", record.Version).
			Build()
	}
	payload, err := unmarshalPayload(record.Kind, record.Payload)
	if err != nil {
		return alfred.RunEvent{}, errors.B.Op(op).Textf("decode %q payload", record.Kind).Err(err).Build()
	}
	return alfred.RunEvent{
		ID:         alfred.RunEventID(record.ID),
		RunID:      alfred.RunID(record.RunID),
		Sequence:   record.Sequence,
		OccurredAt: record.OccurredAt,
		Visibility: record.Visibility,
		Payload:    payload,
	}, nil
}

func marshalPayload(payload alfred.RunEventPayload) (json.RawMessage, error) {
	const op = "marshalPayload"

	var record any
	switch payload := payload.(type) {
	case alfred.RunStateChanged:
		record = struct {
			Status alfred.RunStatus `json:"status"`
		}{Status: payload.Status}
	case alfred.MessageAdded:
		message, err := marshalMessage(payload.Message)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		record = struct {
			Message     messageRecord `json:"message"`
			ModelCallID string        `json:"model_call_id,omitempty"`
		}{Message: message, ModelCallID: string(payload.ModelCallID)}
	case alfred.ToolCallRequested:
		record = struct {
			ModelCallID string         `json:"model_call_id"`
			Call        toolCallRecord `json:"call"`
		}{ModelCallID: string(payload.ModelCallID), Call: marshalToolCall(payload.Call)}
	case alfred.ToolCallCompleted:
		result, err := marshalToolResult(payload.Result)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		record = struct {
			Result toolResultRecord `json:"result"`
		}{Result: result}
	case alfred.PolicyEvaluated:
		record = struct {
			CallID   string               `json:"call_id"`
			Decision policyDecisionRecord `json:"decision"`
		}{CallID: string(payload.CallID), Decision: marshalPolicyDecision(payload.Decision)}
	case alfred.ApprovalRequested:
		record = struct {
			Request approvalRequestRecord `json:"request"`
		}{Request: marshalApprovalRequest(payload.Request)}
	case alfred.ApprovalResolved:
		record = struct {
			Resolution approvalResolutionRecord `json:"resolution"`
		}{Resolution: marshalApprovalResolution(payload.Resolution)}
	case alfred.InputRequested:
		request, err := marshalInputRequest(payload.Request)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		record = struct {
			Request inputRequestRecord `json:"request"`
		}{Request: request}
	case alfred.InputReceived:
		response, err := marshalInputResponse(payload.Response)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		record = struct {
			Response inputResponseRecord `json:"response"`
		}{Response: response}
	case alfred.ModelCallRecorded:
		record = struct {
			Metrics modelCallMetricsRecord `json:"metrics"`
		}{Metrics: marshalModelCallMetrics(payload.Metrics)}
	case alfred.ModelCallCompleted:
		output, err := marshalModelOutput(payload.Output)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		record = struct {
			CallID string               `json:"call_id"`
			Output *[]modelOutputRecord `json:"output,omitempty"`
		}{CallID: string(payload.CallID), Output: output}
	case alfred.RunInterrupted:
		record = struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		}{Reason: payload.Reason, Message: payload.Message}
	case alfred.ContextCompacted:
		summary, err := marshalContent(payload.Summary)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		record = struct {
			FromEventID    string          `json:"from_event_id"`
			ThroughEventID string          `json:"through_event_id"`
			Summary        []contentRecord `json:"summary"`
			ModelCallID    string          `json:"model_call_id"`
		}{string(payload.FromEventID), string(payload.ThroughEventID), summary, string(payload.ModelCallID)}
	default:
		return nil, errors.B.
			Op(op).
			KindInvalidInput().
			Textf("unsupported run event payload %T", payload).
			Build()
	}

	data, err := json.Marshal(record)
	if err != nil {
		return nil, errors.B.Op(op).Textf("marshal %T", record).Err(err).Build()
	}
	return data, nil
}

func marshalMessage(message alfred.Message) (messageRecord, error) {
	content, err := marshalContent(message.Content)
	return messageRecord{Role: message.Role, Content: content}, err
}

func marshalContent(parts []alfred.ContentPart) ([]contentRecord, error) {
	const op = "marshalContent"

	if parts == nil {
		return nil, nil
	}
	records := make([]contentRecord, 0, len(parts))
	for _, part := range parts {
		switch part := part.(type) {
		case alfred.TextContent:
			records = append(records, contentRecord{Kind: alfred.ContentKindText, Text: part.Text})
		case alfred.ResourceContent:
			if part.Type == alfred.ContentKindText {
				return nil, errors.B.Op(op).KindInvalidInput().Text("text content cannot contain a resource").Build()
			}
			records = append(records, contentRecord{
				Kind: part.Type,
				Resource: &resourceRecord{
					URI:       part.Resource.URI,
					MediaType: part.Resource.MediaType,
					Name:      part.Resource.Name,
				},
			})
		default:
			return nil, errors.B.Op(op).KindInvalidInput().Textf("unsupported content part %T", part).Build()
		}
	}
	return records, nil
}

func marshalToolCall(call alfred.ToolCall) toolCallRecord {
	return toolCallRecord{string(call.ID), string(call.RunID), string(call.ToolID), call.Arguments}
}

func marshalToolResult(result alfred.ToolResult) (toolResultRecord, error) {
	output, err := marshalContent(result.Output)
	return toolResultRecord{CallID: string(result.CallID), Output: output, IsError: result.IsError}, err
}

func marshalPolicyDecision(decision alfred.PolicyDecision) policyDecisionRecord {
	return policyDecisionRecord{decision.Outcome, decision.Reason, decision.RuleID}
}

func marshalApprovalRequest(request alfred.ApprovalRequest) approvalRequestRecord {
	return approvalRequestRecord{string(request.ID), string(request.CallID), request.ActionDigest, request.Reason}
}

func marshalApprovalResolution(resolution alfred.ApprovalResolution) approvalResolutionRecord {
	return approvalResolutionRecord{string(resolution.ApprovalID), resolution.Outcome, resolution.Reason}
}

func marshalInputRequest(request alfred.InputRequest) (inputRequestRecord, error) {
	prompt, err := marshalContent(request.Prompt)
	return inputRequestRecord{ID: string(request.ID), CallID: string(request.CallID), Prompt: prompt}, err
}

func marshalInputResponse(response alfred.InputResponse) (inputResponseRecord, error) {
	content, err := marshalContent(response.Content)
	return inputResponseRecord{RequestID: string(response.RequestID), Content: content}, err
}

func marshalModelCallMetrics(metrics alfred.ModelCallMetrics) modelCallMetricsRecord {
	return modelCallMetricsRecord{
		CallID:  string(metrics.CallID),
		Model:   modelRefRecord{string(metrics.Model.ProviderID), string(metrics.Model.ModelID)},
		Outcome: metrics.Outcome,
		Usage: tokenUsageRecord{
			InputTokens: metrics.Usage.InputTokens, CachedInputTokens: metrics.Usage.CachedInputTokens,
			OutputTokens: metrics.Usage.OutputTokens, ReasoningTokens: metrics.Usage.ReasoningTokens,
			Additional: metrics.Usage.Additional,
		},
		Cost: modelCostRecord{
			Currency: metrics.Cost.Currency, InputMicros: metrics.Cost.InputMicros,
			OutputMicros: metrics.Cost.OutputMicros, TotalMicros: metrics.Cost.TotalMicros,
			PricingID: metrics.Cost.PricingID, Estimated: metrics.Cost.Estimated,
		},
		Timing: modelCallTimingRecord{
			DispatchedAt: metrics.Timing.DispatchedAt, CompletedAt: metrics.Timing.CompletedAt,
			Duration:          metrics.Timing.Duration,
			TimeToFirstEvent:  metrics.Timing.TimeToFirstEvent,
			TimeToFirstOutput: metrics.Timing.TimeToFirstOutput,
		},
	}
}
