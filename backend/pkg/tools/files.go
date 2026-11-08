package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

const maxFileBytes = 2 * 1024 * 1024

func openProject(root string) (*os.Root, error) { return os.OpenRoot(root) }

func readProjectFile(root *os.Root, path string) ([]byte, error) {
	const op = "readProjectFile"
	if !filepath.IsLocal(path) {
		return nil, errors.B.Op(op).Text("Use a path relative to the project directory.").Build()
	}
	info, err := root.Stat(path)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	if !info.Mode().IsRegular() {
		return nil, errors.B.Op(op).Text("Read requires a regular text file.").Build()
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, errors.B.Op(op).Text("File must be a regular text file no larger than 2 MiB.").Build()
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	if len(data) > maxFileBytes || bytes.ContainsRune(data, 0) || !utf8.Valid(data) {
		return nil, errors.B.Op(op).Text("File is binary or exceeds 2 MiB.").Build()
	}
	return data, nil
}

// NewRead reads numbered lines from a project-relative file.
func NewRead() alfred.Tool {
	return define("read", "Read", "Read a project-relative text file with line numbers. Defaults to the first 200 lines; at most 2000 lines per call.",
		`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string"
    },
    "start_line": {
      "type": "integer"
    },
    "limit": {
      "type": "integer"
    }
  },
  "required": [
    "path"
  ],
  "additionalProperties": false
}`, readFile)
}

func readFile(ctx context.Context, invocation alfred.ToolInvocation) (alfred.ToolOutcome, error) {
	var args struct {
		Path  string `json:"path"`
		Start int    `json:"start_line"`
		Limit int    `json:"limit"`
	}
	args.Start, args.Limit = 1, 200
	if !decodeArguments(invocation.Call, &args) || args.Start < 1 || args.Limit < 1 || args.Limit > 2000 {
		return textResult(invocation.Call, "Provide path, start_line >= 1, and limit between 1 and 2000.", true), nil
	}
	root, err := openProject(invocation.ProjectRoot)
	if err != nil {
		return textResult(invocation.Call, err.Error(), true), nil
	}
	defer func() { _ = root.Close() }()
	data, err := readProjectFile(root, args.Path)
	if err != nil {
		return textResult(invocation.Call, err.Error(), true), nil
	}
	lines := strings.Split(string(data), "\n")
	var output strings.Builder
	for index := args.Start - 1; index < len(lines) && index < args.Start-1+args.Limit; index++ {
		if err := ctx.Err(); err != nil {
			return alfred.ToolOutcome{}, err
		}
		_, _ = fmt.Fprintf(&output, "%d: %s\n", index+1, lines[index])
	}
	if args.Start-1+args.Limit < len(lines) {
		output.WriteString("[More lines available; use start_line to continue]\n")
	}
	return textResult(invocation.Call, output.String(), false), nil
}

// NewGlob finds project-relative paths using glob patterns, including **.
func NewGlob() alfred.Tool {
	return define("glob", "Glob", "Find files by a project-relative glob, e.g. **/*.go. Skips .git; returns at most 200 paths.",
		`{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string"
    }
  },
  "required": [
    "pattern"
  ],
  "additionalProperties": false
}`, globFiles)
}

// globPattern maps **/*.go to any depth (including root), src/* to one level,
// and file?.go to exactly one non-separator character. Other characters are literal.
func globPattern(pattern string) (*regexp.Regexp, error) {
	var result strings.Builder
	result.WriteString("^")
	runes := []rune(pattern)
	for index := 0; index < len(runes); index++ {
		switch runes[index] {
		case '*':
			if index+1 < len(runes) && runes[index+1] == '*' {
				index++
				if index+1 < len(runes) && runes[index+1] == '/' {
					result.WriteString("(?:.*/)?")
					index++
				} else {
					result.WriteString(".*")
				}
			} else {
				result.WriteString("[^/]*")
			}
		case '?':
			result.WriteString("[^/]")
		default:
			result.WriteString(regexp.QuoteMeta(string(runes[index])))
		}
	}
	return regexp.Compile(result.String() + "$")
}

//nolint:nilerr // Invalid model arguments are tool results so the model can correct them.
func globFiles(ctx context.Context, invocation alfred.ToolInvocation) (alfred.ToolOutcome, error) {
	var args struct {
		Pattern string `json:"pattern"`
	}
	if !decodeArguments(invocation.Call, &args) || args.Pattern == "" || !filepath.IsLocal(args.Pattern) {
		return textResult(invocation.Call, "Provide a glob pattern.", true), nil
	}
	pattern, err := globPattern(args.Pattern)
	if err != nil {
		return textResult(invocation.Call, "Invalid glob pattern.", true), nil
	}
	return walkProject(ctx, invocation, func(root *os.Root, path string) (string, bool) {
		if pattern.MatchString(path) {
			return path + "\n", true
		}
		return "", false
	})
}

// NewGrep searches project text files using Go regular expressions.
func NewGrep() alfred.Tool {
	return define("grep", "Grep", "Search project text files with a regular expression. Returns path:line:text, at most 200 matching files. "+
		"Skips .git, binary files and files over 2 MiB.",
		`{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string"
    },
    "path": {
      "type": "string"
    }
  },
  "required": [
    "pattern"
  ],
  "additionalProperties": false
}`, grepFiles)
}

//nolint:nilerr // Invalid model arguments are tool results so the model can correct them.
func grepFiles(ctx context.Context, invocation alfred.ToolInvocation) (alfred.ToolOutcome, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if !decodeArguments(invocation.Call, &args) || args.Pattern == "" {
		return textResult(invocation.Call, "Provide a regular expression.", true), nil
	}
	if args.Path != "" {
		if !filepath.IsLocal(args.Path) {
			return textResult(invocation.Call, "Use a project-relative search path.", true), nil
		}
		args.Path = filepath.ToSlash(filepath.Clean(args.Path))
		if args.Path == "." {
			args.Path = ""
		}
	}
	pattern, err := regexp.Compile(args.Pattern)
	if err != nil {
		return textResult(invocation.Call, "Invalid regular expression: "+err.Error(), true), nil
	}
	return walkProject(ctx, invocation, func(root *os.Root, path string) (string, bool) {
		if args.Path != "" && path != args.Path && !strings.HasPrefix(path, strings.TrimSuffix(args.Path, "/")+"/") {
			return "", false
		}
		return grepFile(root, path, pattern)
	})
}

func grepFile(root *os.Root, path string, pattern *regexp.Regexp) (string, bool) {
	data, err := readProjectFile(root, path)
	if err != nil {
		return "", false
	}
	var output strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), maxFileBytes+1)
	for line := 1; scanner.Scan(); line++ {
		if pattern.MatchString(scanner.Text()) {
			_, _ = fmt.Fprintf(&output, "%s:%d:%s\n", path, line, scanner.Text())
		}
		if output.Len() >= maxOutputBytes {
			break
		}
	}
	return output.String(), output.Len() != 0
}

func walkProject(ctx context.Context, invocation alfred.ToolInvocation, visit func(*os.Root, string) (string, bool)) (alfred.ToolOutcome, error) {
	root, err := openProject(invocation.ProjectRoot)
	if err != nil {
		return textResult(invocation.Call, err.Error(), true), nil
	}
	defer func() { _ = root.Close() }()
	var output strings.Builder
	matches, files := 0, 0
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		files++
		text, matched := visit(root, path)
		if matched {
			output.WriteString(text)
			matches++
		}
		if matches >= 200 || files >= 20000 || output.Len() >= maxOutputBytes {
			output.WriteString("[Results truncated]\n")
			return fs.SkipAll
		}
		return nil
	})
	if ctx.Err() != nil {
		return alfred.ToolOutcome{}, ctx.Err()
	}
	if err != nil {
		return textResult(invocation.Call, "Search failed: "+err.Error(), true), nil
	}
	if output.Len() == 0 {
		output.WriteString("No matches.")
	}
	return textResult(invocation.Call, output.String(), false), nil
}

// NewEdit creates a file or replaces exactly one occurrence of old_text.
func NewEdit() alfred.Tool {
	return define("edit", "Edit", "Replace exactly one occurrence of old_text in a project file. Read first. "+
		"Empty old_text creates a new file and never overwrites an existing one.",
		`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string"
    },
    "old_text": {
      "type": "string"
    },
    "new_text": {
      "type": "string"
    }
  },
  "required": [
    "path",
    "old_text",
    "new_text"
  ],
  "additionalProperties": false
}`, editFile)
}

func editFile(ctx context.Context, invocation alfred.ToolInvocation) (alfred.ToolOutcome, error) {
	var args struct {
		Path string  `json:"path"`
		Old  *string `json:"old_text"`
		New  *string `json:"new_text"`
	}
	if !decodeArguments(invocation.Call, &args) || !filepath.IsLocal(args.Path) || args.Old == nil || args.New == nil {
		return textResult(invocation.Call, "Provide a project-relative path, old_text and new_text.", true), nil
	}
	root, err := openProject(invocation.ProjectRoot)
	if err != nil {
		return textResult(invocation.Call, err.Error(), true), nil
	}
	defer func() { _ = root.Close() }()
	text := *args.New
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if *args.Old != "" {
		data, err := readProjectFile(root, args.Path)
		if err != nil {
			return textResult(invocation.Call, err.Error(), true), nil
		}
		if strings.Count(string(data), *args.Old) != 1 {
			return textResult(invocation.Call, "old_text must match exactly once. Read the file and provide a unique match.", true), nil
		}
		text = strings.Replace(string(data), *args.Old, *args.New, 1)
		flags = os.O_WRONLY | os.O_TRUNC
	}
	if err := ctx.Err(); err != nil {
		return alfred.ToolOutcome{}, err
	}
	file, err := root.OpenFile(args.Path, flags, 0o644)
	if err != nil {
		return textResult(invocation.Call, err.Error(), true), nil
	}
	_, err = file.WriteString(text)
	closeErr := file.Close()
	if err != nil {
		return textResult(invocation.Call, err.Error(), true), nil
	}
	if closeErr != nil {
		return textResult(invocation.Call, closeErr.Error(), true), nil
	}
	return textResult(invocation.Call, "Updated "+args.Path, false), nil
}
