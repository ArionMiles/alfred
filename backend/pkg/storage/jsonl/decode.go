package jsonl

import (
	"encoding/json"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

//nolint:gocognit // The explicit kind switch is the durable schema's exhaustive decoding boundary.
func unmarshalPayload(kind alfred.RunEventKind, data json.RawMessage) (alfred.RunEventPayload, error) {
	const op = "unmarshalPayload"

	decode := func(record any) error {
		if err := json.Unmarshal(data, record); err != nil {
			return errors.B.Op(op).KindInvalidInput().Textf("decode %q payload", kind).Err(err).Build()
		}
		return nil
	}

	switch kind {
	case alfred.RunEventKindStateChanged:
		var record struct {
			Status alfred.RunStatus `json:"status"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		return alfred.RunStateChanged{Status: record.Status}, nil
	case alfred.RunEventKindMessageAdded:
		var record struct {
			Message     messageRecord `json:"message"`
			ModelCallID string        `json:"model_call_id"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		message, err := unmarshalMessage(record.Message)
		return alfred.MessageAdded{Message: message, ModelCallID: alfred.ModelCallID(record.ModelCallID)}, err
	case alfred.RunEventKindToolCallRequested:
		var record struct {
			ModelCallID string         `json:"model_call_id"`
			Call        toolCallRecord `json:"call"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		return alfred.ToolCallRequested{
			ModelCallID: alfred.ModelCallID(record.ModelCallID),
			Call:        unmarshalToolCall(record.Call),
		}, nil
	case alfred.RunEventKindToolCallCompleted:
		var record struct {
			Result toolResultRecord `json:"result"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		result, err := unmarshalToolResult(record.Result)
		return alfred.ToolCallCompleted{Result: result}, err
	case alfred.RunEventKindPolicyEvaluated:
		var record struct {
			CallID   string               `json:"call_id"`
			Decision policyDecisionRecord `json:"decision"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		return alfred.PolicyEvaluated{
			CallID:   alfred.ToolCallID(record.CallID),
			Decision: unmarshalPolicyDecision(record.Decision),
		}, nil
	case alfred.RunEventKindApprovalRequested:
		var record struct {
			Request approvalRequestRecord `json:"request"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		return alfred.ApprovalRequested{Request: unmarshalApprovalRequest(record.Request)}, nil
	case alfred.RunEventKindApprovalResolved:
		var record struct {
			Resolution approvalResolutionRecord `json:"resolution"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		return alfred.ApprovalResolved{Resolution: unmarshalApprovalResolution(record.Resolution)}, nil
	case alfred.RunEventKindInputRequested:
		var record struct {
			Request inputRequestRecord `json:"request"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		request, err := unmarshalInputRequest(record.Request)
		return alfred.InputRequested{Request: request}, err
	case alfred.RunEventKindInputReceived:
		var record struct {
			Response inputResponseRecord `json:"response"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		response, err := unmarshalInputResponse(record.Response)
		return alfred.InputReceived{Response: response}, err
	case alfred.RunEventKindModelCallRecorded:
		var record struct {
			Metrics modelCallMetricsRecord `json:"metrics"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		return alfred.ModelCallRecorded{Metrics: unmarshalModelCallMetrics(record.Metrics)}, nil
	case alfred.RunEventKindModelCallCompleted:
		var record struct {
			CallID string               `json:"call_id"`
			Output *[]modelOutputRecord `json:"output"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		output, err := unmarshalModelOutput(record.Output)
		return alfred.ModelCallCompleted{CallID: alfred.ModelCallID(record.CallID), Output: output}, err
	case alfred.RunEventKindInterrupted:
		var record struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		return alfred.RunInterrupted{Reason: record.Reason, Message: record.Message}, nil
	case alfred.RunEventKindContextCompacted:
		var record struct {
			FromEventID    string          `json:"from_event_id"`
			ThroughEventID string          `json:"through_event_id"`
			Summary        []contentRecord `json:"summary"`
			ModelCallID    string          `json:"model_call_id"`
		}
		if err := decode(&record); err != nil {
			return nil, err
		}
		summary, err := unmarshalContent(record.Summary)
		return alfred.ContextCompacted{
			FromEventID:    alfred.RunEventID(record.FromEventID),
			ThroughEventID: alfred.RunEventID(record.ThroughEventID),
			Summary:        summary,
			ModelCallID:    alfred.ModelCallID(record.ModelCallID),
		}, err
	default:
		return nil, errors.B.
			Op(op).
			KindInvalidInput().
			Textf("unsupported run event kind %q", kind).
			Build()
	}
}

func unmarshalMessage(record messageRecord) (alfred.Message, error) {
	content, err := unmarshalContent(record.Content)
	return alfred.Message{Role: record.Role, Content: content}, err
}

func unmarshalContent(records []contentRecord) ([]alfred.ContentPart, error) {
	const op = "unmarshalContent"

	if records == nil {
		return nil, nil
	}
	parts := make([]alfred.ContentPart, 0, len(records))
	for _, record := range records {
		kind := record.Kind
		switch kind {
		case alfred.ContentKindText:
			parts = append(parts, alfred.TextContent{Text: record.Text})
		case alfred.ContentKindImage, alfred.ContentKindAudio, alfred.ContentKindVideo, alfred.ContentKindFile:
			if record.Resource == nil {
				return nil, errors.B.
					Op(op).
					KindInvalidInput().
					Textf("%q content has no resource", kind).
					Build()
			}
			parts = append(parts, alfred.ResourceContent{
				Type: kind,
				Resource: alfred.ResourceRef{
					URI:       record.Resource.URI,
					MediaType: record.Resource.MediaType,
					Name:      record.Resource.Name,
				},
			})
		default:
			return nil, errors.B.
				Op(op).
				KindInvalidInput().
				Textf("unsupported content kind %q", record.Kind).
				Build()
		}
	}
	return parts, nil
}

func unmarshalToolCall(record toolCallRecord) alfred.ToolCall {
	return alfred.ToolCall{
		ID: alfred.ToolCallID(record.ID), RunID: alfred.RunID(record.RunID),
		ToolID: alfred.ToolID(record.ToolID), Arguments: record.Arguments,
	}
}

func unmarshalToolResult(record toolResultRecord) (alfred.ToolResult, error) {
	output, err := unmarshalContent(record.Output)
	return alfred.ToolResult{CallID: alfred.ToolCallID(record.CallID), Output: output, IsError: record.IsError}, err
}

func unmarshalPolicyDecision(record policyDecisionRecord) alfred.PolicyDecision {
	return alfred.PolicyDecision{Outcome: record.Outcome, Reason: record.Reason, RuleID: record.RuleID}
}

func unmarshalApprovalRequest(record approvalRequestRecord) alfred.ApprovalRequest {
	return alfred.ApprovalRequest{
		ID: alfred.ApprovalID(record.ID), CallID: alfred.ToolCallID(record.CallID),
		ActionDigest: record.ActionDigest, Reason: record.Reason,
	}
}

func unmarshalApprovalResolution(record approvalResolutionRecord) alfred.ApprovalResolution {
	return alfred.ApprovalResolution{
		ApprovalID: alfred.ApprovalID(record.ApprovalID),
		Outcome:    record.Outcome, Reason: record.Reason,
	}
}

func unmarshalInputRequest(record inputRequestRecord) (alfred.InputRequest, error) {
	prompt, err := unmarshalContent(record.Prompt)
	return alfred.InputRequest{ID: alfred.InputRequestID(record.ID), CallID: alfred.ToolCallID(record.CallID), Prompt: prompt}, err
}

func unmarshalInputResponse(record inputResponseRecord) (alfred.InputResponse, error) {
	content, err := unmarshalContent(record.Content)
	return alfred.InputResponse{RequestID: alfred.InputRequestID(record.RequestID), Content: content}, err
}

func unmarshalModelCallMetrics(record modelCallMetricsRecord) alfred.ModelCallMetrics {
	return alfred.ModelCallMetrics{
		CallID: alfred.ModelCallID(record.CallID),
		Model: alfred.ModelRef{
			ProviderID: alfred.ProviderID(record.Model.ProviderID),
			ModelID:    alfred.ModelID(record.Model.ModelID),
		},
		Outcome: record.Outcome,
		Usage: alfred.TokenUsage{
			InputTokens: record.Usage.InputTokens, CachedInputTokens: record.Usage.CachedInputTokens,
			OutputTokens: record.Usage.OutputTokens, ReasoningTokens: record.Usage.ReasoningTokens,
			Additional: record.Usage.Additional,
		},
		Cost: alfred.ModelCost{
			Currency: record.Cost.Currency, InputMicros: record.Cost.InputMicros,
			OutputMicros: record.Cost.OutputMicros, TotalMicros: record.Cost.TotalMicros,
			PricingID: record.Cost.PricingID, Estimated: record.Cost.Estimated,
		},
		Timing: alfred.ModelCallTiming{
			DispatchedAt: record.Timing.DispatchedAt, CompletedAt: record.Timing.CompletedAt,
			Duration:          record.Timing.Duration,
			TimeToFirstEvent:  record.Timing.TimeToFirstEvent,
			TimeToFirstOutput: record.Timing.TimeToFirstOutput,
		},
	}
}
