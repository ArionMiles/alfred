package rpc

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/pkg/events"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// encodeSessionEvent projects only readable output from internal checkpoints.
// Tracking checkpoints also suppresses their later MessageAdded copies on replay.
func encodeSessionEvent(event alfred.RunEvent, committed map[alfred.ModelCallID]bool) (*alfredv1.RunEvent, error) {
	const op = "encodeSessionEvent"
	if completion, ok := event.Payload.(alfred.ModelCallCompleted); ok {
		if completion.Output == nil {
			return nil, nil
		}
		committed[completion.CallID] = true
		output := &alfredv1.ModelOutputCompleted{ModelCallId: string(completion.CallID)}
		for _, item := range completion.Output {
			switch payload := item.Payload.(type) {
			case alfred.Message:
				message, err := encodeMessage(payload)
				if err != nil {
					return nil, errors.B.Op(op).Err(err).Build()
				}
				output.Output = append(output.Output, &alfredv1.ModelOutputItem{Payload: &alfredv1.ModelOutputItem_Message{Message: message}})
			case alfred.Reasoning:
				if payload.Text != "" {
					output.Output = append(output.Output, &alfredv1.ModelOutputItem{Payload: &alfredv1.ModelOutputItem_Reasoning{
						Reasoning: &alfredv1.Reasoning{Text: payload.Text, TextKind: encodeReasoningTextKind(payload.TextKind)},
					}})
				}
			}
		}
		return &alfredv1.RunEvent{
			Id: string(event.ID), RunId: string(event.RunID), Sequence: event.Sequence, OccurredAt: timestamppb.New(event.OccurredAt),
			Payload: &alfredv1.RunEvent_ModelOutputCompleted{ModelOutputCompleted: output},
		}, nil
	}
	if added, ok := event.Payload.(alfred.MessageAdded); ok && committed[added.ModelCallID] {
		return nil, nil
	}
	if event.Visibility != alfred.RunEventVisibilityUser {
		return nil, nil
	}
	return encodeRunEvent(event)
}

func encodeReasoningTextKind(kind alfred.ReasoningTextKind) alfredv1.ReasoningTextKind {
	switch kind {
	case alfred.ReasoningTextReasoning:
		return alfredv1.ReasoningTextKind_REASONING_TEXT_KIND_REASONING
	case alfred.ReasoningTextSummary:
		return alfredv1.ReasoningTextKind_REASONING_TEXT_KIND_SUMMARY
	default:
		return alfredv1.ReasoningTextKind_REASONING_TEXT_KIND_UNSPECIFIED
	}
}

func encodeRunEvent(event alfred.RunEvent) (*alfredv1.RunEvent, error) {
	const op = "encodeRunEvent"

	encoded := &alfredv1.RunEvent{
		Id:         string(event.ID),
		RunId:      string(event.RunID),
		Sequence:   event.Sequence,
		OccurredAt: timestamppb.New(event.OccurredAt),
	}
	var err error
	switch payload := event.Payload.(type) {
	case alfred.RunStateChanged:
		var status alfredv1.RunStatus
		status, err = encodeRunStatus(payload.Status)
		encoded.Payload = &alfredv1.RunEvent_RunStateChanged{
			RunStateChanged: &alfredv1.RunStateChanged{Status: status},
		}
	case alfred.MessageAdded:
		var message *alfredv1.Message
		message, err = encodeMessage(payload.Message)
		encoded.Payload = &alfredv1.RunEvent_MessageAdded{
			MessageAdded: &alfredv1.MessageAdded{Message: message},
		}
	case alfred.ToolCallRequested:
		encoded.Payload = &alfredv1.RunEvent_ToolCallRequested{
			ToolCallRequested: &alfredv1.ToolCallRequested{
				ModelCallId: string(payload.ModelCallID),
				Call:        encodeToolCall(payload.Call),
			},
		}
	case alfred.ToolCallCompleted:
		var result *alfredv1.ToolResult
		result, err = encodeToolResult(payload.Result)
		encoded.Payload = &alfredv1.RunEvent_ToolCallCompleted{
			ToolCallCompleted: &alfredv1.ToolCallCompleted{Result: result},
		}
	case alfred.ApprovalRequested:
		encoded.Payload = &alfredv1.RunEvent_ApprovalRequested{
			ApprovalRequested: &alfredv1.ApprovalRequested{Request: &alfredv1.ApprovalRequest{
				Id:     string(payload.Request.ID),
				CallId: string(payload.Request.CallID),
				Reason: payload.Request.Reason,
			}},
		}
	case alfred.ApprovalResolved:
		var outcome alfredv1.ApprovalOutcome
		outcome, err = encodeApprovalOutcome(payload.Resolution.Outcome)
		encoded.Payload = &alfredv1.RunEvent_ApprovalResolved{
			ApprovalResolved: &alfredv1.ApprovalResolved{Resolution: &alfredv1.ApprovalResolution{
				ApprovalId: string(payload.Resolution.ApprovalID),
				Outcome:    outcome,
				Reason:     payload.Resolution.Reason,
			}},
		}
	case alfred.InputRequested:
		var prompt []*alfredv1.ContentPart
		prompt, err = encodeContentParts(payload.Request.Prompt)
		encoded.Payload = &alfredv1.RunEvent_InputRequested{
			InputRequested: &alfredv1.InputRequested{Request: &alfredv1.RunInputRequest{
				Id:     string(payload.Request.ID),
				CallId: string(payload.Request.CallID),
				Prompt: prompt,
			}},
		}
	case alfred.InputReceived:
		var content []*alfredv1.ContentPart
		content, err = encodeContentParts(payload.Response.Content)
		encoded.Payload = &alfredv1.RunEvent_InputReceived{
			InputReceived: &alfredv1.InputReceived{Response: &alfredv1.RunInputResponse{
				RequestId: string(payload.Response.RequestID),
				Content:   content,
			}},
		}
	case alfred.RunInterrupted:
		encoded.Payload = &alfredv1.RunEvent_RunInterrupted{
			RunInterrupted: &alfredv1.RunInterrupted{Reason: payload.Reason, Message: payload.Message},
		}
	default:
		return nil, errors.B.
			Op(op).
			Textf("unsupported user-visible run event payload %T", event.Payload).
			Build()
	}
	if err != nil {
		return nil, errors.B.Op(op).Textf("encode %T payload", event.Payload).Err(err).Build()
	}
	return encoded, nil
}

func encodeUpdateActivity(activity events.UpdateActivity) (*alfredv1.RunUpdate, bool, error) {
	const op = "encodeUpdateActivity"

	switch activity := activity.(type) {
	case events.UpdateGap:
		return nil, true, nil
	case events.TransientUpdate:
		if activity.Update.Visibility != alfred.RunEventVisibilityUser {
			return nil, false, nil
		}
		update, err := encodeRunUpdate(activity.Update)
		if err != nil {
			return nil, false, errors.B.Op(op).Err(err).Build()
		}
		return update, false, nil
	default:
		return nil, false, errors.B.Op(op).Textf("unsupported update activity %T", activity).Build()
	}
}

func encodeRunUpdate(update alfred.RunUpdate) (*alfredv1.RunUpdate, error) {
	const op = "encodeRunUpdate"

	encoded := &alfredv1.RunUpdate{RunId: string(update.RunID)}
	switch payload := update.Payload.(type) {
	case alfred.RunReasoningDelta:
		encoded.Payload = &alfredv1.RunUpdate_ReasoningDelta{ReasoningDelta: &alfredv1.RunReasoningDelta{
			ModelCallId: string(payload.CallID), Index: payload.Delta.Index,
			Text: payload.Delta.Text, TextKind: encodeReasoningTextKind(payload.Delta.TextKind),
		}}
	case alfred.RunContentDelta:
		delta, err := encodeContentPart(payload.Delta.Delta)
		if err != nil {
			return nil, errors.B.Op(op).Text("encode content delta").Err(err).Build()
		}
		encoded.Payload = &alfredv1.RunUpdate_ContentDelta{ContentDelta: &alfredv1.RunContentDelta{
			ModelCallId: string(payload.CallID),
			Index:       payload.Delta.Index,
			Delta:       delta,
		}}
	default:
		return nil, errors.B.Op(op).Textf("unsupported run update payload %T", update.Payload).Build()
	}
	return encoded, nil
}

func encodeMessage(message alfred.Message) (*alfredv1.Message, error) {
	const op = "encodeMessage"

	var role alfredv1.MessageRole
	switch message.Role {
	case alfred.MessageRoleUser:
		role = alfredv1.MessageRole_MESSAGE_ROLE_USER
	case alfred.MessageRoleAssistant:
		role = alfredv1.MessageRole_MESSAGE_ROLE_ASSISTANT
	default:
		return nil, errors.B.Op(op).Textf("unknown message role %q", message.Role).Build()
	}
	content, err := encodeContentParts(message.Content)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	return &alfredv1.Message{Role: role, Content: content}, nil
}

func encodeToolCall(call alfred.ToolCall) *alfredv1.ToolCall {
	return &alfredv1.ToolCall{
		Id:            string(call.ID),
		RunId:         string(call.RunID),
		ToolId:        string(call.ToolID),
		ArgumentsJson: append([]byte(nil), call.Arguments...),
	}
}

func encodeToolResult(result alfred.ToolResult) (*alfredv1.ToolResult, error) {
	const op = "encodeToolResult"

	output, err := encodeContentParts(result.Output)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	return &alfredv1.ToolResult{
		CallId:  string(result.CallID),
		Output:  output,
		IsError: result.IsError,
	}, nil
}

func encodeContentParts(parts []alfred.ContentPart) ([]*alfredv1.ContentPart, error) {
	const op = "encodeContentParts"

	encoded := make([]*alfredv1.ContentPart, 0, len(parts))
	for index, part := range parts {
		value, err := encodeContentPart(part)
		if err != nil {
			return nil, errors.B.Op(op).Textf("encode content part %d", index).Err(err).Build()
		}
		encoded = append(encoded, value)
	}
	return encoded, nil
}

func encodeContentPart(part alfred.ContentPart) (*alfredv1.ContentPart, error) {
	const op = "encodeContentPart"

	switch part := part.(type) {
	case alfred.TextContent:
		return &alfredv1.ContentPart{Value: &alfredv1.ContentPart_Text{Text: part.Text}}, nil
	case alfred.ResourceContent:
		kind, err := encodeContentKind(part.Type)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		return &alfredv1.ContentPart{Value: &alfredv1.ContentPart_Resource{
			Resource: &alfredv1.ResourceContent{
				Kind: kind,
				Resource: &alfredv1.ResourceRef{
					Uri:       part.Resource.URI,
					MediaType: part.Resource.MediaType,
					Name:      part.Resource.Name,
				},
			},
		}}, nil
	default:
		return nil, errors.B.Op(op).Textf("unsupported content part %T", part).Build()
	}
}

func encodeContentKind(kind alfred.ContentKind) (alfredv1.ContentKind, error) {
	const op = "encodeContentKind"

	switch kind {
	case alfred.ContentKindImage:
		return alfredv1.ContentKind_CONTENT_KIND_IMAGE, nil
	case alfred.ContentKindAudio:
		return alfredv1.ContentKind_CONTENT_KIND_AUDIO, nil
	case alfred.ContentKindVideo:
		return alfredv1.ContentKind_CONTENT_KIND_VIDEO, nil
	case alfred.ContentKindFile:
		return alfredv1.ContentKind_CONTENT_KIND_FILE, nil
	default:
		return alfredv1.ContentKind_CONTENT_KIND_UNSPECIFIED, errors.B.
			Op(op).
			Textf("unknown resource content kind %q", kind).
			Build()
	}
}

func encodeApprovalOutcome(outcome alfred.ApprovalOutcome) (alfredv1.ApprovalOutcome, error) {
	const op = "encodeApprovalOutcome"

	switch outcome {
	case alfred.ApprovalOutcomeApproved:
		return alfredv1.ApprovalOutcome_APPROVAL_OUTCOME_APPROVED, nil
	case alfred.ApprovalOutcomeDenied:
		return alfredv1.ApprovalOutcome_APPROVAL_OUTCOME_DENIED, nil
	default:
		return alfredv1.ApprovalOutcome_APPROVAL_OUTCOME_UNSPECIFIED, errors.B.
			Op(op).
			Textf("unknown approval outcome %q", outcome).
			Build()
	}
}
