package tools

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/ArionMiles/alfred"
)

const maxOutputBytes = 64 * 1024

type builtin struct {
	definition alfred.ToolDefinition
	invoke     func(context.Context, alfred.ToolInvocation) (alfred.ToolOutcome, error)
}

func (tool *builtin) Definition() alfred.ToolDefinition { return tool.definition }
func (tool *builtin) Invoke(ctx context.Context, invocation alfred.ToolInvocation) (alfred.ToolOutcome, error) {
	return tool.invoke(ctx, invocation)
}

func define(id, name, description, schema string, invoke func(context.Context, alfred.ToolInvocation) (alfred.ToolOutcome, error)) alfred.Tool {
	return &builtin{
		definition: alfred.ToolDefinition{ID: alfred.ToolID(id), Name: name, Description: description, InputSchema: json.RawMessage(schema)},
		invoke:     invoke,
	}
}

func textResult(call alfred.ToolCall, text string, failed bool) alfred.ToolOutcome {
	text = strings.ToValidUTF8(text, "")
	if len(text) > maxOutputBytes {
		text = strings.ToValidUTF8(text[:maxOutputBytes], "") + "\n[Output truncated]"
	}
	return alfred.ToolOutcome{Result: &alfred.ToolResult{CallID: call.ID, Output: []alfred.ContentPart{alfred.TextContent{Text: text}}, IsError: failed}}
}

func decodeArguments(call alfred.ToolCall, target any) bool {
	decoder := json.NewDecoder(strings.NewReader(string(call.Arguments)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

// NewAskUser asks one free-text question through the client's existing input gate.
func NewAskUser() alfred.Tool {
	return define("ask_user", "AskUser", "Ask the user a question when their input is needed to continue.",
		`{
  "type": "object",
  "properties": {
    "question": {
      "type": "string"
    }
  },
  "required": [
    "question"
  ],
  "additionalProperties": false
}`,
		func(_ context.Context, invocation alfred.ToolInvocation) (alfred.ToolOutcome, error) {
			var args struct {
				Question string `json:"question"`
			}
			if !decodeArguments(invocation.Call, &args) || strings.TrimSpace(args.Question) == "" {
				return textResult(invocation.Call, "Provide a nonempty question.", true), nil
			}
			return alfred.ToolOutcome{InputPrompt: []alfred.ContentPart{alfred.TextContent{Text: args.Question}}}, nil
		})
}
