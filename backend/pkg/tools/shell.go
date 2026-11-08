package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/ArionMiles/alfred"
)

// Config contains file-facing settings for built-in tools.
type Config struct {
	Shell     ShellConfig     `koanf:"shell"`
	WebSearch WebSearchConfig `koanf:"web_search"`
}

// ShellConfig selects the optional, explicitly sourced agent initialization file.
type ShellConfig struct {
	RCFile string `koanf:"rc_file"`
}

// DefaultConfig supplies tool settings without requiring a configuration file.
func DefaultConfig() Config { return Config{WebSearch: WebSearchConfig{Provider: exaName}} }

// NewShell starts a fresh zsh with an explicit environment for each invocation.
func NewShell(config ShellConfig, home string) alfred.Tool {
	return define("shell", "Shell", "Run a command in a fresh non-interactive zsh, starting in the project directory. "+
		"The configured agent rc file is sourced automatically. Shell state does not persist between calls. "+
		"Default timeout 120 seconds; maximum 600.",
		`{
  "type": "object",
  "properties": {
    "command": {
      "type": "string"
    },
    "timeout_seconds": {
      "type": "integer"
    }
  },
  "required": [
    "command"
  ],
  "additionalProperties": false
}`,
		func(ctx context.Context, invocation alfred.ToolInvocation) (alfred.ToolOutcome, error) {
			return runShell(ctx, invocation, config, home)
		})
}

func runShell(ctx context.Context, invocation alfred.ToolInvocation, config ShellConfig, home string) (alfred.ToolOutcome, error) {
	var args struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	args.Timeout = 120
	if !decodeArguments(invocation.Call, &args) || strings.TrimSpace(args.Command) == "" || args.Timeout < 1 || args.Timeout > 600 {
		return textResult(invocation.Call, "Provide a command and timeout_seconds between 1 and 600.", true), nil
	}
	binary := ""
	for _, path := range []string{"/bin/zsh", "/usr/bin/zsh"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			binary = path
			break
		}
	}
	if binary == "" {
		return textResult(invocation.Call, "Install zsh at /bin/zsh or /usr/bin/zsh to use Shell.", true), nil
	}
	commandContext, cancel := context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
	defer cancel()
	// Arguments are passed separately: neither the rc path nor command is interpolated.
	const script = `typeset -r alfred_command="$2"
if [[ -n "$1" ]]; then
  builtin source -- "$1" || exit $?
fi
eval -- "$alfred_command"`
	// #nosec G204 -- Executing model-requested commands is the Shell tool's explicit contract.
	command := exec.CommandContext(commandContext, binary, "-d", "-f", "-c", script, "alfred-shell", config.RCFile, args.Command)
	command.Dir = invocation.ProjectRoot
	command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb", "SHELL=" + binary}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = 2 * time.Second
	output := &boundedOutput{}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	if ctx.Err() != nil {
		return alfred.ToolOutcome{}, ctx.Err()
	}
	text := output.String()
	if commandContext.Err() != nil {
		return textResult(invocation.Call, text+"\n[Command timed out]", true), nil
	}
	if command.ProcessState != nil {
		text += fmt.Sprintf("\n[Exit code: %d]", command.ProcessState.ExitCode())
	}
	if err != nil && command.ProcessState == nil {
		text += "\nCommand could not start."
	}
	return textResult(invocation.Call, text, err != nil), nil
}

type boundedOutput struct {
	builder   strings.Builder
	truncated bool
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	count := min(len(data), max(maxOutputBytes-256-output.builder.Len(), 0))
	_, _ = output.builder.Write(data[:count])
	output.truncated = output.truncated || count < len(data)
	return len(data), nil
}

func (output *boundedOutput) String() string {
	text := output.builder.String()
	if output.truncated {
		text += "\n[Output truncated]"
	}
	return text
}
