package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArionMiles/alfred"
)

func requireZsh(t *testing.T) {
	t.Helper()
	for _, path := range []string{"/bin/zsh", "/usr/bin/zsh"} {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
	t.Skip("zsh is not installed")
}

func TestShellUsesCustomRCWithoutServerEnvironmentOrUserStartup(t *testing.T) {
	requireZsh(t)
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("ALFRED_TEST_SERVER_SECRET", "must-not-inherit")
	t.Setenv("PATH", "/server-private-path")
	writeToolFile(t, filepath.Join(home, ".zshenv"), "export ALFRED_TEST_USER_STARTUP=loaded\n")
	rc := filepath.Join(home, "custom ' init.zsh")
	writeToolFile(t, rc, "export AGENT_SETTING=ready\nagent_function() { print -r -- custom-function; }\n")
	tool := NewShell(ShellConfig{RCFile: rc}, home)
	result := invokeTool(t, tool, root, map[string]any{"command": "print -r -- $AGENT_SETTING; agent_function; print -r -- ${ALFRED_TEST_SERVER_SECRET-unset}; print -r -- ${ALFRED_TEST_USER_STARTUP-unset}; pwd; print -r -- $PATH"})
	text := resultText(result)
	if result.Result.IsError || !strings.Contains(text, "ready\ncustom-function\nunset\nunset") || strings.Contains(text, "must-not-inherit") || strings.Contains(text, "server-private-path") || !strings.Contains(text, root) {
		t.Fatalf("shell=%s", text)
	}
	first := invokeTool(t, tool, root, map[string]any{"command": "export LOCAL_ONLY=yes"})
	second := invokeTool(t, tool, root, map[string]any{"command": "print -r -- ${LOCAL_ONLY-unset}"})
	if first.Result.IsError || !strings.Contains(resultText(second), "unset") {
		t.Fatal("shell state persisted between calls")
	}
}

func TestShellTimeoutCancellationAndRCFailure(t *testing.T) {
	t.Parallel()
	requireZsh(t)
	root := t.TempDir()
	tool := NewShell(ShellConfig{}, root)
	result := invokeTool(t, tool, root, map[string]any{"command": "sleep 30 & wait", "timeout_seconds": 1})
	if !result.Result.IsError || !strings.Contains(resultText(result), "timed out") {
		t.Fatalf("timeout=%v", result.Result)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	args, _ := json.Marshal(map[string]any{"command": "sleep 30 & wait"})
	started := time.Now()
	_, err := tool.Invoke(ctx, alfred.ToolInvocation{ProjectRoot: root, Call: alfred.ToolCall{ID: "cancel", Arguments: args}})
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("cancellation=%v", err)
	}
	marker := filepath.Join(root, "should-not-exist")
	failed := invokeTool(t, NewShell(ShellConfig{RCFile: filepath.Join(root, "missing.zsh")}, root), root, map[string]any{"command": "touch " + marker})
	if !failed.Result.IsError {
		t.Fatal("missing rc did not fail")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("command ran after rc failure")
	}
	output := invokeTool(t, tool, root, map[string]any{"command": "printf '%100000s' x"})
	if len(resultText(output)) > maxOutputBytes || !strings.Contains(resultText(output), "Output truncated") {
		t.Fatal("shell output was not bounded")
	}
}
