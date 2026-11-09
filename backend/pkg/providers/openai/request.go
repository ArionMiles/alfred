package openai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type preparedRequest struct {
	params responses.ResponseNewParams
	tools  map[string]alfred.ToolID
}

func (provider *Provider) buildRequest(ctx context.Context, request alfred.ModelRequest) (preparedRequest, error) {
	const op = "buildRequest"

	if request.Model.ProviderID != provider.id {
		return preparedRequest{}, errors.B.Op(op).KindInvalidArgument().Text("model targets another provider").Build()
	}
	model, err := provider.ResolveModel(ctx, request.Model.ModelID)
	if err != nil {
		return preparedRequest{}, errors.B.Op(op).Err(err).Build()
	}
	prepared := preparedRequest{
		params: responses.ResponseNewParams{
			Model: shared.ResponsesModel(request.Model.ModelID), Instructions: param.NewOpt(request.Instructions),
			Store: param.NewOpt(false), Include: []responses.ResponseIncludable{"reasoning.encrypted_content"},
		}, tools: make(map[string]alfred.ToolID, len(request.Tools)),
	}
	if model.Capabilities.Reasoning {
		prepared.params.Reasoning = shared.ReasoningParam{Summary: "auto"}
	}
	names := make(map[alfred.ToolID]string, len(request.Tools))
	var subscriptionTools []responses.NamespaceToolToolUnionParam
	for _, tool := range request.Tools {
		if _, exists := prepared.tools[tool.Name]; exists || tool.Name == "" {
			return preparedRequest{}, errors.B.Op(op).KindInvalidArgument().Text("tool names must be nonempty and unique").Build()
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil || schema == nil {
			return preparedRequest{}, errors.B.Op(op).KindInvalidArgument().Text("tool input schema must be a JSON object").Build()
		}
		if provider.subscription != nil {
			subscriptionTools = append(subscriptionTools, responses.NamespaceToolToolUnionParam{
				OfFunction: &responses.NamespaceToolToolFunctionParam{
					Name: tool.Name, Description: param.NewOpt(tool.Description), Parameters: schema, Strict: param.NewOpt(false),
				},
			})
		} else {
			prepared.params.Tools = append(prepared.params.Tools, responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{
				Name: tool.Name, Description: param.NewOpt(tool.Description), Parameters: schema, Strict: param.NewOpt(false),
			}})
		}
		prepared.tools[tool.Name], names[tool.ID] = tool.ID, tool.Name
	}
	if len(subscriptionTools) != 0 {
		prepared.params.Tools = append(prepared.params.Tools, responses.ToolUnionParam{OfNamespace: &responses.NamespaceToolParam{
			Name: "alfred", Description: "Tools available from Alfred", Tools: subscriptionTools,
		}})
	}
	input := make(responses.ResponseInputParam, 0, len(request.Input))
	for _, item := range request.Input {
		encoded, err := encodeInput(item, names)
		if err != nil {
			return preparedRequest{}, errors.B.Op(op).Err(err).Build()
		}
		input = append(input, encoded)
	}
	prepared.params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: input}
	return prepared, nil
}

func encodeInput(input alfred.ModelInput, names map[alfred.ToolID]string) (responses.ResponseInputItemUnionParam, error) {
	const op = "encodeInput"

	switch item := input.Payload.(type) {
	case alfred.Message:
		if item.Role != alfred.MessageRoleUser && item.Role != alfred.MessageRoleAssistant {
			return responses.ResponseInputItemUnionParam{}, errors.B.Op(op).KindInvalidArgument().Text("unsupported message role").Build()
		}
		text, err := textContent(item.Content)
		if err != nil {
			return responses.ResponseInputItemUnionParam{}, err
		}
		if data, ok := input.Native[nativeKey("message")]; ok {
			return replayNative(data, "message")
		}
		return responses.ResponseInputItemUnionParam{OfMessage: &responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRole(item.Role),
			Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(text)},
		}}, nil
	case alfred.ToolCall:
		name, ok := names[item.ToolID]
		if !ok {
			return responses.ResponseInputItemUnionParam{}, errors.B.Op(op).KindNotFound().UserMsg("A historical tool is no longer available.").Build()
		}
		if !json.Valid(item.Arguments) {
			return responses.ResponseInputItemUnionParam{}, errors.B.Op(op).KindInvalidArgument().Text("tool arguments are not JSON").Build()
		}
		if data, ok := input.Native[nativeKey("function_call")]; ok {
			return replayNative(data, "function_call")
		}
		return responses.ResponseInputItemUnionParam{OfFunctionCall: &responses.ResponseFunctionToolCallParam{
			CallID: string(item.ID), Name: name, Arguments: string(item.Arguments),
		}}, nil
	case alfred.ToolResult:
		text, err := textContent(item.Output)
		if err != nil {
			return responses.ResponseInputItemUnionParam{}, err
		}
		if item.IsError {
			text = "Tool error:\n" + text
		}
		return responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
			CallID: param.NewOpt(string(item.CallID)),
			Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{OfString: param.NewOpt(text)},
		}}, nil
	case alfred.Reasoning:
		data, ok := input.Native[nativeKey("reasoning")]
		if !ok {
			return responses.ResponseInputItemUnionParam{}, errors.B.Op(op).KindUnimplemented().
				UserMsg("This reasoning item cannot be replayed through the OpenAI Responses adapter.").Build()
		}
		return replayNative(data, "reasoning")
	default:
		return responses.ResponseInputItemUnionParam{}, errors.B.Op(op).KindUnimplemented().Text("unsupported model input").Build()
	}
}

func textContent(content []alfred.ContentPart) (string, error) {
	const op = "textContent"

	var text strings.Builder
	for _, part := range content {
		value, ok := part.(alfred.TextContent)
		if !ok {
			return "", errors.B.Op(op).KindUnimplemented().UserMsg("The OpenAI adapter currently accepts text content only.").Build()
		}
		text.WriteString(value.Text)
	}
	return text.String(), nil
}

func replayNative(data json.RawMessage, kind string) (responses.ResponseInputItemUnionParam, error) {
	const op = "replayNative"

	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil || header.Type != kind {
		return responses.ResponseInputItemUnionParam{}, errors.B.Op(op).KindInvalidInput().Text("invalid native Responses item").Build()
	}
	return param.Override[responses.ResponseInputItemUnionParam](data), nil
}
