package tools

import (
	"context"
	"testing"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type testTool struct {
	definition alfred.ToolDefinition
}

func (tool *testTool) Definition() alfred.ToolDefinition {
	return tool.definition
}

func (*testTool) Invoke(_ context.Context, invocation alfred.ToolInvocation) (alfred.ToolOutcome, error) {
	return alfred.ToolOutcome{Result: &alfred.ToolResult{CallID: invocation.Call.ID}}, nil
}

func TestRegistryResolvesTool(t *testing.T) {
	want := &testTool{definition: alfred.ToolDefinition{ID: "read", Name: "Read"}}
	registry, err := NewRegistry(want)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	got, err := registry.ResolveTool(t.Context(), "read")
	if err != nil {
		t.Fatalf("ResolveTool() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveTool() = %p, want %p", got, want)
	}
}

func TestRegistryListsToolsByID(t *testing.T) {
	registry, err := NewRegistry(
		&testTool{definition: alfred.ToolDefinition{ID: "write", Name: "Write"}},
		&testTool{definition: alfred.ToolDefinition{ID: "read", Name: "Read"}},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	got, err := registry.ListTools(t.Context())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListTools() returned %d definitions, want 2", len(got))
	}
	if got[0].ID != "read" || got[1].ID != "write" {
		t.Fatalf("ListTools() IDs = [%q, %q], want [read, write]", got[0].ID, got[1].ID)
	}
}

func TestNewRegistryRejectsDuplicateToolID(t *testing.T) {
	_, err := NewRegistry(
		&testTool{definition: alfred.ToolDefinition{ID: "read"}},
		&testTool{definition: alfred.ToolDefinition{ID: "read"}},
	)
	if err == nil {
		t.Fatal("NewRegistry() error = nil, want conflict")
	}
	if got := errors.WhatKind(err); got != errors.Conflict {
		t.Fatalf("errors.WhatKind() = %#v, want %#v", got, errors.Conflict)
	}
}

func TestRegistryReportsMissingTool(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	_, err = registry.ResolveTool(t.Context(), "missing")
	if err == nil {
		t.Fatal("ResolveTool() error = nil, want not found")
	}
	if got := errors.WhatKind(err); got != errors.NotFound {
		t.Fatalf("errors.WhatKind() = %#v, want %#v", got, errors.NotFound)
	}
}

var (
	_ alfred.ToolResolver = (*Registry)(nil)
	_ alfred.ToolCatalog  = (*Registry)(nil)
)
