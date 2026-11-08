<h1 align="center">Alfred</h1>

<p align="center">
  <a href="#quick-start">Quick Start</a> ·
  <a href="#guides">Guides</a> ·
  <a href="#features">Features</a> ·
  <a href="#releases">Releases</a>
</p>

Alfred is a pre-alpha personal agent with a Go core, a local background service,
and a terminal client. The TUI supports conversations, streamed answers and
reasoning, project sessions, and tools for files, commands, and web research.

The core is designed for use as an SDK. Model providers, tools, credential
storage, and execution policy are expressed as Go interfaces; the application
composes their implementations explicitly.

Linux and macOS are supported. OpenAI's Responses API is the current inference
adapter; Exa supplies web search. Other model-provider setup forms exist, but
their inference adapters are not available. Memory, scheduling, MCP, browser
automation, and subscription sign-in are not implemented.

## Quick Start

Install [Go](https://go.dev/dl/) at the version specified in
[backend/go.mod](backend/go.mod), [Task](https://taskfile.dev/installation/),
and zsh. From the repository root:

```sh
task build
```

Start Alfred from the project you want to work in:

```sh
cd /path/to/your/project
/path/to/alfred/bin/alfred
```

In the TUI:

1. Open `/providers`, choose OpenAI, choose API key, and save your key.
2. Open `/models` and select a model.
3. Send a message.

For web search, open `/web-search` and save an Exa API key. Use Ctrl+P to open
the command palette and `/quit` to exit. Alfred starts its background service
automatically; quitting the TUI leaves it running.

## Guides

- [Terminal client](docs/tui.md): commands, sessions, scrolling, and input requests.
- [Config](docs/config.md): server and client file locations, defaults, and settings.
- [Deployment](docs/deployment.md): run the daemon with Docker Compose and connect clients.
- [Model providers](docs/providers.md): connecting OpenAI and choosing a model.
- [Tools and instructions](docs/tools.md): search setup, Shell, and instruction files.
- [Architecture](docs/ARCHITECTURE.md): design decisions, tradeoffs, and constraints.
- [Agent state machine](docs/agent-state-machine.html): run states and durable replay.

## Features

- Embed the Go core in your own application with explicit dependency composition.
- Bring your own model providers, tools, credential stores, and execution policies
  through focused Go interfaces.
- Build clients on a shared service API; execution continues independently of
  client connections.
- Preserve execution history and resume interrupted runs from durable checkpoints.
- Start with built-in tools for files, shell commands, and web research, or supply
  your own.

## Releases

Alfred is pre-alpha. Tagged releases publish Linux and macOS archives for amd64
and arm64, SHA-256 checksums, and a Homebrew cask in
[ArionMiles/tap](https://github.com/ArionMiles/homebrew-tap).

After the first release from this codebase is published:

```sh
brew install --cask ArionMiles/tap/alfred
```

Use an up-to-date Homebrew. macOS binaries are not notarized and may require
explicit approval before execution.

Container images are published to `ghcr.io/arionmiles/alfred`: version tags for
releases, `latest` for stable releases, and `tip` plus `tip-<sha>` after CI passes
on `main`. See [Deployment](docs/deployment.md) for Compose instructions.
