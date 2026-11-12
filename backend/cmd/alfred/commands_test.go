package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArionMiles/alfred/internal/buildinfo"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestBareCommandShowsUsageWithoutLoadingConfiguration(t *testing.T) {
	t.Setenv("ALFRED_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.toml"))
	command := newRootCommand()
	var output, errorOutput bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&errorOutput)
	command.SetArgs([]string{})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if text := output.String(); !strings.Contains(text, "Usage:") || !strings.Contains(text, "serve") || !strings.Contains(text, "tui") {
		t.Fatalf("usage output = %q", text)
	}
	if errorOutput.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", errorOutput.String())
	}
	for _, child := range command.Commands() {
		if !child.Hidden && child.Name() != "tui" && child.Name() != "serve" {
			t.Fatalf("unexpected command %q", child.Name())
		}
	}
}

func TestExecutableCommandsLoadConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	t.Setenv("ALFRED_CONFIG_FILE", path)
	for _, name := range []string{"tui", "serve"} {
		t.Run(name, func(t *testing.T) {
			command := newRootCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs([]string{name})
			if err := command.ExecuteContext(t.Context()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s did not load configuration: %v", name, err)
			}
			if output.Len() != 0 {
				t.Fatalf("command printed an error before the exit boundary: %s", output.String())
			}
		})
	}
}

func TestCommandsRejectArgumentsBeforeLoadingConfiguration(t *testing.T) {
	t.Setenv("ALFRED_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.toml"))
	for _, arguments := range [][]string{
		{"unknown"},
		{"ser"},
		{"tu"},
		{"help"},
		{"completion"},
		{"serve", "extra"},
		{"tui", "extra"},
		{"--unknown"},
		{"serve", "--unknown"},
		{"tui", "--unknown"},
	} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			command := newRootCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs(arguments)
			err := command.ExecuteContext(t.Context())
			if err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("arguments %q were not rejected before config loading: %v", arguments, err)
			}
			if output.Len() != 0 {
				t.Fatalf("argument error printed before exit boundary: %s", output.String())
			}
		})
	}
}

func TestHelpAndVersionDoNotLoadConfiguration(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "missing"
		if malformed {
			name = "malformed"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if malformed {
				if err := os.WriteFile(path, []byte("[broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("ALFRED_CONFIG_FILE", path)
			for _, test := range []struct {
				arguments []string
				contains  string
			}{
				{arguments: []string{"--help"}, contains: "Alfred personal agent"},
				{arguments: []string{"tui", "--help"}, contains: "Open the terminal client"},
				{arguments: []string{"serve", "--help"}, contains: "Run the background service in the foreground"},
				{arguments: []string{"--version"}, contains: "alfred version " + buildinfo.Version + "\n"},
			} {
				t.Run(strings.Join(test.arguments, " "), func(t *testing.T) {
					command := newRootCommand()
					var output, errorOutput bytes.Buffer
					command.SetOut(&output)
					command.SetErr(&errorOutput)
					command.SetArgs(test.arguments)
					if err := command.ExecuteContext(t.Context()); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(output.String(), test.contains) || errorOutput.Len() != 0 {
						t.Fatalf("stdout=%q, stderr=%q", output.String(), errorOutput.String())
					}
				})
			}
		})
	}
}
