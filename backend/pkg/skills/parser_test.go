package skills

import (
	"testing"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestParseSkillFile(t *testing.T) {
	t.Parallel()

	data := []byte(`---
name: code-review
description: Review code changes for correctness.
license: Apache-2.0
compatibility: Requires git.
metadata:
  author: example
allowed-tools: Bash(git:*) Read
---
# Code review

Inspect the diff before commenting.
`)

	definition, instructions, err := parseSkillFile("/skills/code-review/SKILL.md", data)
	if err != nil {
		t.Fatalf("parseSkillFile() error = %v", err)
	}

	want := alfred.SkillDefinition{
		Name:          "code-review",
		Description:   "Review code changes for correctness.",
		Location:      "/skills/code-review/SKILL.md",
		License:       "Apache-2.0",
		Compatibility: "Requires git.",
		Metadata:      map[string]string{"author": "example"},
		AllowedTools:  "Bash(git:*) Read",
	}
	if definition.Name != want.Name ||
		definition.Description != want.Description ||
		definition.Location != want.Location ||
		definition.License != want.License ||
		definition.Compatibility != want.Compatibility ||
		definition.Metadata["author"] != want.Metadata["author"] ||
		definition.AllowedTools != want.AllowedTools {
		t.Fatalf("parseSkillFile() definition = %#v, want %#v", definition, want)
	}

	const wantInstructions = "# Code review\n\nInspect the diff before commenting."
	if instructions != wantInstructions {
		t.Fatalf("parseSkillFile() instructions = %q, want %q", instructions, wantInstructions)
	}
}

func TestParseSkillFileRejectsInvalidFrontmatter(t *testing.T) {
	t.Parallel()

	tests := map[string][]byte{
		"missing delimiters":  []byte("name: code-review\n"),
		"invalid yaml":        []byte("---\nname: [\n---\n"),
		"missing name":        []byte("---\ndescription: Reviews code.\n---\n"),
		"missing description": []byte("---\nname: code-review\n---\n"),
	}

	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := parseSkillFile("/skills/code-review/SKILL.md", data)
			if err == nil {
				t.Fatal("parseSkillFile() error = nil")
			}
			if kind := errors.WhatKind(err); kind != errors.InvalidInput {
				t.Fatalf("errors.WhatKind(error) = %#v, want %#v", kind, errors.InvalidInput)
			}
		})
	}
}
