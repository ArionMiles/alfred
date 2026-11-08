# Tools and instructions

Alfred can inspect and edit files, run commands, search the web, fetch pages,
and ask you a question. Describe your task in the conversation; Alfred chooses
tools and uses their results to continue. Tool calls and results appear in the
conversation. Tool errors let Alfred correct its request and try again.

## Available tools

| Tool | What it does |
| --- | --- |
| Read | Reads a project-relative text file with line numbers. Defaults to 200 lines; can request up to 2,000. |
| Glob | Finds files with `*`, `**`, and `?` patterns, such as `**/*.go`. |
| Grep | Searches file contents with regular expressions, optionally within a project-relative file or directory. |
| Edit | Replaces one exact text match, or creates a new file. An ambiguous match fails without changing the file. |
| Shell | Runs a command in a fresh zsh, starting in the project directory. |
| WebSearch | Searches using a configured search provider. Exa is currently available. |
| WebFetch | Retrieves text or raw HTML from an HTTP(S) URL; does not run JavaScript. |
| AskUser | Pauses work and presents a question for you to answer in the TUI. |

Read and Edit require paths inside the project, including when following
symlinks. Read and edits of existing files require text files no larger than
2 MiB. Glob and Grep skip `.git` and symlinks;
Grep also skips binary and unreadable files. Searches stop after 200 matching
files or 20,000 visited files. Tool output is limited to approximately 64 KiB,
with a truncation notice when needed. Read accepts a starting line for continuing
a long file. These filesystem limits do not restrict commands run through Shell.

For conversation controls, input requests, and session navigation, see
[Terminal client](tui.md).

All tools currently use Alfred's default allow policy. There is no per-call
confirmation by default.

CallUser and UseBrowser are not included.

## Set up Shell

Install zsh at `/bin/zsh` or `/usr/bin/zsh`. Alfred starts each command in a
fresh, non-interactive shell, with a basic system PATH and your home directory.
It does not copy the server's environment variables, credentials, or PATH.

To initialize tools and paths for every command, create a file such as
`~/.config/alfred/shell.zsh`:

```zsh
export PATH="/opt/homebrew/bin:$HOME/.local/bin:$PATH"
export AWS_PROFILE="alfred"
```

Then add this to the server's `config.toml`:

```toml
[tools.shell]
rc_file = "shell.zsh"
```

Relative paths resolve beside `config.toml`; absolute paths are also accepted.
The path is literal: `~` and environment references are not expanded in TOML.
Omit `rc_file` to use the basic environment without additional initialization.
A configured file that cannot be sourced fails that command.

Alfred sources this file before each command. Functions and environment changes
are available to that command; shell state does not carry into the next call.
Keep the file suitable for non-interactive use, without prompts or terminal UI.
File-content edits apply to subsequent commands. Changing its configured path
requires restarting the service.

Ordinary user startup files are skipped. Zsh still reads its installation's
system `zshenv` file, as described in the
[zsh startup documentation](https://zsh.sourceforge.io/Doc/Release/Files.html).
The clean environment does not isolate filesystem or network access: commands
run as your OS user.

Commands default to a 120-second timeout and may request up to 600 seconds.
Cancellation and timeout terminate the command's process group. Background
processes in that group are cleaned up when the command ends.

## Set up web search

Run `/web-search` or press Ctrl+P and choose Web search. Select Exa, enter your
API key in the masked field, and select Save. Alfred remembers it; do not put
the key in TOML or your Shell initialization file for WebSearch. Exa is a search service and does not add
entries to the model picker. Saving does not verify account access.

Use Up/Down or Tab/Shift+Tab to choose a search backend. In the form, Tab and
Shift+Tab move between fields, Save, and Cancel. Enter advances from a field;
on Save it submits the form. Esc cancels an unfinished setup. After submitting,
wait for the save to finish. A failed save keeps your inputs for retry; a
successful save clears the secret input. Reopening a saved setup shows only
non-secret settings and requires reentering the API key to replace it. Opening
and closing through the palette preserves your conversation draft.

Web search setup is independent of Providers, which configures model services.

Exa is the default search provider. To specify the default explicitly:

```toml
[tools.web_search]
provider = "exa"
```

WebSearch supports multiple installed search providers and can choose one for
an individual request. Currently Exa is the only built-in implementation; another
provider needs its own adapter before its name can be selected in configuration.
Exa searches return up to ten results with titles, URLs, and bounded excerpts.
See [Exa's API documentation](https://exa.ai/docs/reference/search) for the service.

## Give Alfred standing instructions

Create an `AGENTS.md` file in your project root for project guidance, for example:

```markdown
Run the relevant tests after changing code.
Keep changes focused and preserve existing formatting.
```

For preferences across projects, create
`$XDG_CONFIG_HOME/alfred/AGENTS.md`, defaulting to
`~/.config/alfred/AGENTS.md` on Linux and macOS.

At each location, Alfred reads `AGENTS.md` if present, otherwise `CLAUDE.md`.
It does not combine both filenames at one location. An empty `AGENTS.md` still
takes precedence over `CLAUDE.md`. Missing files are optional; an existing file
that cannot be read produces an error.

Alfred includes its built-in instructions, global guidance, project guidance,
and available skill descriptions in that order. Each instruction file is
labelled with its path. Project guidance specializes global preferences; your
conversation instructions take precedence. Instruction files are independent
of the project skill-trust setting.

Files are read when a run starts or resumes. Edits take effect the next time
context is built; they do not alter an already-running model call. Discovery
currently covers the global directory and project root, not nested directories.
Changing `ALFRED_CONFIG_FILE` does not move the global instruction directory.
