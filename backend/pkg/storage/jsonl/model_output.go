package jsonl

import (
	"encoding/json"
	"strings"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type modelOutputRecord struct {
	Kind alfred.ModelInputKind `json:"kind"`
	// Byte values preserve each opaque JSON fragment exactly on disk.
	Native    map[string][]byte `json:"native,omitempty"`
	Message   *messageRecord    `json:"message,omitempty"`
	ToolCall  *toolCallRecord   `json:"tool_call,omitempty"`
	Reasoning *reasoningRecord  `json:"reasoning,omitempty"`
}

type reasoningRecord struct {
	Text         string                    `json:"text,omitempty"`
	TextKind     alfred.ReasoningTextKind  `json:"text_kind,omitempty"`
	Continuation *legacyContinuationRecord `json:"continuation,omitempty"`
}

// legacyContinuationRecord reads the reasoning-only attachment in version 1.
type legacyContinuationRecord struct {
	Format string `json:"format"`
	// Bytes preserve the provider JSON exactly, including whitespace and escaping.
	// encoding/json represents this opaque value as a base64 string on disk.
	Data []byte `json:"data"`
}

func marshalModelOutput(output []alfred.ModelInput) (*[]modelOutputRecord, error) {
	const op = "marshalModelOutput"

	if output == nil {
		return nil, nil
	}
	records := make([]modelOutputRecord, 0, len(output))
	for _, input := range output {
		native, err := marshalNative(input.Native)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		record := modelOutputRecord{Native: native}
		switch item := input.Payload.(type) {
		case alfred.Message:
			if item.Role != alfred.MessageRoleAssistant {
				return nil, errors.B.Op(op).KindInvalidInput().Text("model output message is not assistant-authored").Build()
			}
			message, err := marshalMessage(item)
			if err != nil {
				return nil, errors.B.Op(op).Err(err).Build()
			}
			record.Kind, record.Message = alfred.ModelInputKindMessage, &message
		case alfred.ToolCall:
			call := marshalToolCall(item)
			record.Kind, record.ToolCall = alfred.ModelInputKindToolCall, &call
		case alfred.Reasoning:
			if err := validateReasoning(item, len(input.Native) > 0); err != nil {
				return nil, errors.B.Op(op).Err(err).Build()
			}
			reasoning := &reasoningRecord{Text: item.Text, TextKind: item.TextKind}
			record.Kind, record.Reasoning = alfred.ModelInputKindReasoning, reasoning
		default:
			return nil, errors.B.Op(op).KindInvalidInput().Textf("unsupported model output %T", item).Build()
		}
		records = append(records, record)
	}
	return &records, nil
}

func unmarshalModelOutput(records *[]modelOutputRecord) ([]alfred.ModelInput, error) {
	const op = "unmarshalModelOutput"

	if records == nil {
		return nil, nil
	}
	output := make([]alfred.ModelInput, 0, len(*records))
	for _, record := range *records {
		native, err := unmarshalNative(record)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		item := alfred.ModelInput{Native: native}
		switch {
		case record.Kind == alfred.ModelInputKindMessage && record.Message != nil && record.ToolCall == nil && record.Reasoning == nil:
			message, err := unmarshalMessage(*record.Message)
			if err != nil {
				return nil, errors.B.Op(op).Err(err).Build()
			}
			if message.Role != alfred.MessageRoleAssistant {
				return nil, errors.B.Op(op).KindInvalidInput().Text("model output message is not assistant-authored").Build()
			}
			item.Payload = message
		case record.Kind == alfred.ModelInputKindToolCall && record.ToolCall != nil && record.Message == nil && record.Reasoning == nil:
			item.Payload = unmarshalToolCall(*record.ToolCall)
		case record.Kind == alfred.ModelInputKindReasoning && record.Reasoning != nil && record.Message == nil && record.ToolCall == nil:
			reasoning := alfred.Reasoning{Text: record.Reasoning.Text, TextKind: record.Reasoning.TextKind}
			if err := validateReasoning(reasoning, len(item.Native) > 0); err != nil {
				return nil, errors.B.Op(op).Err(err).Build()
			}
			item.Payload = reasoning
		default:
			return nil, errors.B.Op(op).KindInvalidInput().Text("invalid model output item").Build()
		}
		output = append(output, item)
	}
	return output, nil
}

func validateReasoning(reasoning alfred.Reasoning, hasNative bool) error {
	const op = "validateReasoning"

	if reasoning.TextKind != "" && reasoning.TextKind != alfred.ReasoningTextReasoning && reasoning.TextKind != alfred.ReasoningTextSummary {
		return errors.B.Op(op).KindInvalidInput().Text("unsupported reasoning text kind").Build()
	}
	if reasoning.Text != "" && reasoning.TextKind == "" {
		return errors.B.Op(op).KindInvalidInput().Text("reasoning text requires a text kind").Build()
	}
	if reasoning.Text == "" && !hasNative {
		return errors.B.Op(op).KindInvalidInput().Text("reasoning requires text or native data").Build()
	}
	return nil
}

func marshalNative(native alfred.NativeData) (map[string][]byte, error) {
	const op = "marshalNative"

	if native == nil {
		return nil, nil
	}
	record := make(map[string][]byte, len(native))
	for key, data := range native {
		if strings.TrimSpace(key) == "" || !json.Valid(data) {
			return nil, errors.B.Op(op).KindInvalidInput().Text("native data requires a namespace and valid JSON").Build()
		}
		record[key] = []byte(data)
	}
	return record, nil
}

func unmarshalNative(record modelOutputRecord) (alfred.NativeData, error) {
	const op = "unmarshalNative"

	var native alfred.NativeData
	if record.Native != nil {
		native = make(alfred.NativeData, len(record.Native))
		for key, data := range record.Native {
			native[key] = json.RawMessage(data)
		}
	}
	if record.Reasoning != nil && record.Reasoning.Continuation != nil {
		state := record.Reasoning.Continuation
		if native != nil {
			return nil, errors.B.Op(op).KindInvalidInput().Text("conflicting native and legacy reasoning data").Build()
		}
		native = alfred.NativeData{state.Format: json.RawMessage(state.Data)}
	}
	if _, err := marshalNative(native); err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	return native, nil
}
