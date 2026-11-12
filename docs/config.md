# Config

Alfred uses `config.toml` for server settings and `cli.toml` for terminal
keybindings. Both files are optional when you use the defaults.

Connect model providers with `/providers`, select a model with `/models`, and
configure web search with `/web-search`. Alfred remembers those settings across
restarts. API keys and model selection do not belong in TOML. See
[Model providers](providers.md), [Tools and instructions](tools.md), and the
[Terminal client](tui.md) guide for those workflows.

## File locations

Linux and macOS use the same locations:

| File | Default location |
| --- | --- |
| Server | `$XDG_CONFIG_HOME/alfred/config.toml` |
| Client | `$XDG_CONFIG_HOME/alfred/cli.toml` |

When XDG_CONFIG_HOME is unset or empty, the base is `~/.config`. XDG paths must
be absolute. Missing default files use defaults; existing invalid or unreadable
files produce errors. Unknown TOML settings and incorrect value types are
rejected. Both `alfred tui` and `alfred serve` load and validate both files.
Bare `alfred`, help output, and `alfred --version` do not load configuration;
they work even when a file is missing or invalid.

Set ALFRED_CONFIG_FILE to select a different server file:

```sh
ALFRED_CONFIG_FILE=/path/to/config.toml /path/to/alfred tui
```

An unset or empty value uses the default path. A missing explicitly selected
file is an error. This does not relocate `cli.toml` or global instruction files;
they remain under the XDG configuration directory. There is no separate client
configuration-path environment variable.

## Server settings — config.toml

The supported settings are:

```toml
data_dir = "/path/to/alfred-data"
listen_address = "127.0.0.1:0"

[tools.shell]
rc_file = "shell.zsh"

[tools.web_search]
provider = "exa"

[providers.openai.authorization]
attempt_timeout = "10m"
```

This example shows every supported server setting. Include only the values you
want to change; the example paths are not defaults. Paths and values are literal:
TOML does not expand `~`, shell variables, or `{env:NAME}` references.

### Data directory

The default data directory is `$XDG_DATA_HOME/alfred`, falling back to
`~/.local/share/alfred` when XDG_DATA_HOME is unset or empty. This is the same
on Linux and macOS.

Set `data_dir` to choose another location for conversations and saved settings.
Relative paths resolve from the directory containing `config.toml`; for example,
`data_dir = "."` saves data beside that file. Empty or blank values are invalid.
Changing this setting selects another location; it does not move existing data.

### Daemon listener

`listen_address` selects the daemon's bind address. The default, `127.0.0.1:0`,
uses a free local port discovered automatically by the TUI. Use a fixed port for
an explicitly connected client, or `0.0.0.0:8080` for a container. Loopback and
wildcard hosts are supported. Port `0` asks the OS to choose a free port.

The local TUI discovers the running daemon through its data directory. Inside
the container, run `docker compose exec alfred alfred tui` to connect to that daemon.
See [Deployment](deployment.md) for Compose setup.

### Shell initialization

`tools.shell.rc_file` selects an optional file to source before every Shell
command. Relative paths resolve beside `config.toml`; absolute paths are also
accepted. The default is no custom initialization file. A configured file that
cannot be sourced fails the command.

Use it to initialize agent-specific paths and tools without sourcing them in
every command. See [Set up Shell](tools.md#set-up-shell) for an example and the
shell's environment and execution behavior.

### Web search

`tools.web_search.provider` selects the default installed search backend. Its
default is `exa`, currently the only built-in adapter. An uninstalled backend
name prevents the service from starting.

Enter the Exa key through `/web-search`, independently of model-provider setup.
See [Set up web search](tools.md#set-up-web-search).

### OpenAI browser sign-in

`providers.openai.authorization.attempt_timeout` limits the whole browser sign-in
attempt, including time waiting for you to complete the OpenAI screens. It defaults
to `"10m"`; use a duration such as `"15m"` to allow more time. An expired attempt
closes its callback listener and asks you to start again. This setting does not
change saved credentials or inference request timeouts. See
[Continue with ChatGPT](providers.md#continue-with-chatgpt).

## Client settings — cli.toml

The supported client settings and their defaults are:

```toml
[keybindings]
submit = ["enter"]
interrupt = ["ctrl+c"]
palette = ["ctrl+p"]
```

Submit sends a message or requested answer and activates slash commands.
Interrupt cancels the active run; it does not exit Alfred. Palette opens and
closes the command menu. Use `/quit` to leave the TUI.

To use Ctrl+X for interruption while retaining the other defaults:

```toml
[keybindings]
interrupt = ["ctrl+x"]
```

Each value is an array of terminal key names. Multiple entries provide alternate
keys for the same action. Use lowercase names such as `enter`, `esc`, and
`ctrl+x`; the terminal must deliver the selected combination to Alfred.

Every action needs at least one nonblank key, and submit, interrupt, and palette
cannot share keys. Omitted values retain their defaults. Appearance and theme
settings are not available.

## Apply changes

Quit and reopen the TUI after editing `cli.toml`; the service does not need to
restart. Server-file changes require restarting the service. Quitting the TUI
alone leaves the service running. If running `alfred serve` in the foreground,
stop it and launch it again with the same server file as the client.

For development, `task dev -- --force` rebuilds and restarts the development
service. The task regenerates its own server file containing only
`data_dir = "."`, so edits to that file are overwritten. To test custom server
settings, build Alfred and launch the binary with your own ALFRED_CONFIG_FILE.
The task reads the normal `cli.toml`.

Edits to the contents of an instruction file or configured Shell rc file are
read on the next relevant run or command; changing their contents does not
require restarting the service.

## Environment variables

Precedence is environment overrides, then file values, then defaults. Only the
following settings have environment overrides:

| Variable | File setting |
| --- | --- |
| `ALFRED_DATA_DIR` | `data_dir` |
| `ALFRED_LISTEN_ADDRESS` | `listen_address` |
| `ALFRED_TOOLS_SHELL_RC_FILE` | `tools.shell.rc_file` |
| `ALFRED_TOOLS_WEB_SEARCH_PROVIDER` | `tools.web_search.provider` |
| `ALFRED_TUI_KEYBINDINGS_SUBMIT` | Client `keybindings.submit` |
| `ALFRED_TUI_KEYBINDINGS_INTERRUPT` | Client `keybindings.interrupt` |
| `ALFRED_TUI_KEYBINDINGS_PALETTE` | Client `keybindings.palette` |

Keybinding overrides are comma-separated key names, for example
`ALFRED_TUI_KEYBINDINGS_SUBMIT=enter,ctrl+s`. A set but empty override is an
explicit empty value; validation still applies. Relative path overrides resolve
beside the selected server file, just like TOML paths. Use absolute paths when
launching the client and service from different directories.

`ALFRED_CONFIG_FILE` selects the server file. `XDG_CONFIG_HOME` and `XDG_DATA_HOME`
select the configuration and data bases. Enter provider and search secrets
through their TUI setup flows.

## Troubleshooting

For startup errors, check the reported path, TOML syntax, setting names, and
value types. An explicitly selected server file must exist. Keybinding values
must be nonblank and cannot overlap across actions.

If a shortcut does nothing, check its terminal key name and whether the terminal
intercepts that combination. Reopen the TUI after editing the client file.
