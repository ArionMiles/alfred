package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArionMiles/alfred"
)

func invokeTool(t *testing.T, tool alfred.Tool, root string, args any) alfred.ToolOutcome {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Invoke(t.Context(), alfred.ToolInvocation{ProjectRoot: root, Call: alfred.ToolCall{ID: "call", ToolID: tool.Definition().ID, Arguments: data}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func resultText(outcome alfred.ToolOutcome) string {
	return outcome.Result.Output[0].(alfred.TextContent).Text
}

func writeToolFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadGlobGrepAndEditProjectFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeToolFile(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	writeToolFile(t, filepath.Join(root, "pkg", "code.go"), "package pkg\nfunc Run() {}\n")
	writeToolFile(t, filepath.Join(root, ".git", "hidden.go"), "func hidden() {}")
	writeToolFile(t, filepath.Join(root, "文名.go"), "package example")
	unicodeMatch := resultText(invokeTool(t, NewGlob(), root, map[string]any{"pattern": "文?.go"}))
	if !strings.Contains(unicodeMatch, "文名.go") {
		t.Fatal("glob did not preserve Unicode pattern characters")
	}
	read := invokeTool(t, NewRead(), root, map[string]any{"path": "main.go", "start_line": 2, "limit": 1})
	if read.Result.IsError || !strings.Contains(resultText(read), "2: func main()") || strings.Contains(resultText(read), "1: package") {
		t.Fatalf("read=%v", read.Result)
	}
	glob := resultText(invokeTool(t, NewGlob(), root, map[string]any{"pattern": "**/*.go"}))
	if !strings.Contains(glob, "main.go") || !strings.Contains(glob, "pkg/code.go") || strings.Contains(glob, "hidden") {
		t.Fatalf("glob=%s", glob)
	}
	grep := resultText(invokeTool(t, NewGrep(), root, map[string]any{"pattern": "func (main|Run)", "path": "pkg"}))
	if !strings.Contains(grep, "pkg/code.go:2:func Run") || strings.Contains(grep, "main.go") {
		t.Fatalf("grep=%s", grep)
	}
	edit := invokeTool(t, NewEdit(), root, map[string]any{"path": "main.go", "old_text": "func main() {}", "new_text": "func main() { Run() }"})
	if edit.Result.IsError {
		t.Fatal(resultText(edit))
	}
	data, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil || !strings.Contains(string(data), "Run()") {
		t.Fatalf("file=%s err=%v", data, err)
	}
	created := invokeTool(t, NewEdit(), root, map[string]any{"path": "new.txt", "old_text": "", "new_text": "new"})
	if created.Result.IsError {
		t.Fatal(resultText(created))
	}
	rejected := invokeTool(t, NewEdit(), root, map[string]any{"path": "new.txt", "old_text": "", "new_text": "overwritten"})
	if !rejected.Result.IsError {
		t.Fatal("empty old_text overwrote an existing file")
	}
	writeToolFile(t, filepath.Join(root, "ambiguous.txt"), "same same")
	rejected = invokeTool(t, NewEdit(), root, map[string]any{"path": "ambiguous.txt", "old_text": "same", "new_text": "changed"})
	if !rejected.Result.IsError {
		t.Fatal("ambiguous replacement succeeded")
	}
}

func TestFilesystemToolsRejectPathsOutsideProject(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), filepath.Join(t.TempDir(), "secret")
	writeToolFile(t, outside, "private")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outside, "../secret", "escape"} {
		for _, tool := range []alfred.Tool{NewRead(), NewEdit()} {
			result := invokeTool(t, tool, root, map[string]any{"path": path})
			if tool.Definition().ID == "edit" {
				result = invokeTool(t, tool, root, map[string]any{"path": path, "old_text": "private", "new_text": "changed"})
			}
			if !result.Result.IsError {
				t.Fatalf("%s accepted %s", tool.Definition().Name, path)
			}
		}
	}
}

func TestToolErrorsAreRecoverableAndOutputIsBounded(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeToolFile(t, filepath.Join(root, "large.txt"), strings.Repeat("x", maxOutputBytes*2))
	result := invokeTool(t, NewRead(), root, map[string]any{"path": "large.txt"})
	if !strings.Contains(resultText(result), "Output truncated") {
		t.Fatal("read output was not bounded")
	}
	for _, tool := range []alfred.Tool{NewRead(), NewGrep(), NewGlob(), NewEdit(), NewAskUser()} {
		result := invokeTool(t, tool, root, map[string]any{"unknown": "value"})
		if result.Result == nil || !result.Result.IsError {
			t.Fatalf("%s did not return a model-correctable error", tool.Definition().Name)
		}
	}
	ask := invokeTool(t, NewAskUser(), root, map[string]any{"question": "Which branch?"})
	if ask.Result != nil || len(ask.InputPrompt) != 1 {
		t.Fatalf("ask=%v", ask)
	}
}
