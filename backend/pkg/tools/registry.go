// Package tools provides Alfred's in-process tool registry.
package tools

import (
	"cmp"
	"context"
	"slices"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Registry resolves tools and lists their definitions.
type Registry struct {
	tools map[alfred.ToolID]alfred.Tool
}

// NewRegistry creates a registry containing tools.
func NewRegistry(tools ...alfred.Tool) (*Registry, error) {
	const op = "NewRegistry"

	registered := make(map[alfred.ToolID]alfred.Tool, len(tools))
	for _, tool := range tools {
		id := tool.Definition().ID
		if _, exists := registered[id]; exists {
			return nil, errors.B.
				Op(op).
				KindConflict().
				Textf("tool %q is registered more than once", id).
				UserMsg("a tool is configured more than once").
				Build()
		}
		registered[id] = tool
	}

	return &Registry{tools: registered}, nil
}

// ResolveTool returns the tool registered with id.
func (r *Registry) ResolveTool(_ context.Context, id alfred.ToolID) (alfred.Tool, error) {
	const op = "ResolveTool"

	tool, ok := r.tools[id]
	if !ok {
		return nil, errors.B.
			Op(op).
			KindNotFound().
			Textf("tool %q is not registered", id).
			UserMsg("the requested tool is not available").
			Build()
	}

	return tool, nil
}

// ListTools returns registered tool definitions ordered by ID.
func (r *Registry) ListTools(_ context.Context) ([]alfred.ToolDefinition, error) {
	definitions := make([]alfred.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		definitions = append(definitions, tool.Definition())
	}

	slices.SortFunc(definitions, func(a, b alfred.ToolDefinition) int {
		return cmp.Compare(a.ID, b.ID)
	})

	return definitions, nil
}
