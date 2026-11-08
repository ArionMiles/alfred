package openai

import (
	"encoding/json"
	"strings"

	"github.com/openai/openai-go/v3/responses"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

const responseStatusCompleted = "completed"

func decodeResponse(response responses.Response, tools map[string]alfred.ToolID, runID alfred.RunID) ([]alfred.ModelEvent, error) {
	const op = "decodeResponse"

	if response.Status != responseStatusCompleted || response.ID == "" || !response.JSON.Output.Valid() {
		return nil, errors.B.Op(op).KindBadGateway().Text("invalid completed Responses snapshot").Build()
	}
	events := make([]alfred.ModelEvent, 0, len(response.Output)+2)
	finish := alfred.ModelFinishReasonStop
	for index := range response.Output {
		item := &response.Output[index]
		payload, err := decodeItem(item, tools, runID)
		if err != nil {
			return nil, err
		}
		if item.Type == "function_call" {
			finish = alfred.ModelFinishReasonToolCall
		}
		events = append(events, alfred.ModelEvent{Payload: payload, Native: alfred.NativeData{nativeKey(item.Type): json.RawMessage(item.RawJSON())}})
	}
	if response.JSON.Usage.Valid() {
		usage, err := decodeUsage(response.Usage)
		if err != nil {
			return nil, err
		}
		events = append(events, alfred.ModelEvent{Payload: usage})
	}
	return append(events, alfred.ModelEvent{Payload: alfred.ModelCompleted{Reason: finish, ProviderReason: responseStatusCompleted}}), nil
}

func decodeItem(item *responses.ResponseOutputItemUnion, tools map[string]alfred.ToolID, runID alfred.RunID) (alfred.ModelEventPayload, error) {
	const op = "decodeItem"

	switch item.Type {
	case "message":
		return decodeMessage(item.AsMessage())
	case "function_call":
		call := item.AsFunctionCall()
		id, ok := tools[call.Name]
		if !ok || call.CallID == "" || !json.Valid([]byte(call.Arguments)) || (call.Status != "" && call.Status != responseStatusCompleted) {
			return nil, errors.B.Op(op).KindBadGateway().Text("invalid or unknown Responses tool call").Build()
		}
		return alfred.ModelToolCall{Call: alfred.ToolCall{
			ID: alfred.ToolCallID(call.CallID), RunID: runID, ToolID: id, Arguments: json.RawMessage(call.Arguments),
		}}, nil
	case "reasoning":
		reasoning := item.AsReasoning()
		var texts []string
		kind := alfred.ReasoningTextSummary
		for _, summary := range reasoning.Summary {
			texts = append(texts, summary.Text)
		}
		if len(texts) == 0 {
			kind = alfred.ReasoningTextReasoning
			for _, content := range reasoning.Content {
				texts = append(texts, content.Text)
			}
		}
		if len(texts) == 0 {
			kind = ""
		}
		return alfred.ModelReasoning{Reasoning: alfred.Reasoning{
			Text: strings.Join(texts, "\n\n"), TextKind: kind,
		}}, nil
	default:
		return nil, errors.B.Op(op).KindUnimplemented().UserMsg("OpenAI returned an output type Alfred does not support yet.").Build()
	}
}

func decodeMessage(message responses.ResponseOutputMessage) (alfred.ModelEventPayload, error) {
	const op = "decodeMessage"

	if message.Status != responseStatusCompleted || string(message.Role) != "assistant" || !message.JSON.Content.Valid() {
		return nil, errors.B.Op(op).KindBadGateway().Text("invalid completed Responses message").Build()
	}
	content := make([]alfred.ContentPart, 0, len(message.Content))
	for _, part := range message.Content {
		switch part.Type {
		case "output_text":
			content = append(content, alfred.TextContent{Text: part.Text})
		case "refusal":
			content = append(content, alfred.TextContent{Text: part.Refusal})
		default:
			return nil, errors.B.Op(op).KindUnimplemented().UserMsg("OpenAI returned message content Alfred does not support yet.").Build()
		}
	}
	return alfred.ModelMessage{Message: alfred.Message{Role: alfred.MessageRoleAssistant, Content: content}}, nil
}

func decodeUsage(usage responses.ResponseUsage) (alfred.ModelUsage, error) {
	const op = "decodeUsage"

	cached, reasoning := usage.InputTokensDetails.CachedTokens, usage.OutputTokensDetails.ReasoningTokens
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || cached < 0 || reasoning < 0 || cached > usage.InputTokens || reasoning > usage.OutputTokens {
		return alfred.ModelUsage{}, errors.B.Op(op).KindBadGateway().Text("invalid Responses token usage").Build()
	}
	return alfred.ModelUsage{Tokens: alfred.TokenUsage{
		InputTokens: uint64(usage.InputTokens), CachedInputTokens: uint64(cached),
		OutputTokens: uint64(usage.OutputTokens), ReasoningTokens: uint64(reasoning),
	}}, nil
}
