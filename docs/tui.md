# Terminal client

Run `alfred` from your project directory. It connects to Alfred's background
service and opens the TUI. Use `/sessions` to reopen a conversation from this
project; send a message to start a new one.

For a container deployment, run `docker compose exec alfred alfred`. The TUI
uses the container's current directory and discovers the daemon already running
there. Files and Shell commands operate on the daemon's filesystem. See
[Deployment](deployment.md) for setup.

## Connect and choose a model

Open `/providers`, select OpenAI and its API-key method, then save your key.
Open `/models` to select a model before sending your first message. The model
selection is remembered across sessions and restarts. See
[Model providers](providers.md) for setup, filtering, and selection details.

Web search has its own `/web-search` setup flow. See
[Set up web search](tools.md#set-up-web-search).

## Commands and navigation

Press Ctrl+P to open Commands. Use Down or Tab for the next action, Up or
Shift+Tab for the previous action, and Enter to select. Esc or Ctrl+P dismisses
the palette. You can also type a slash command in the message input.

| Command | Action | Aliases |
| --- | --- | --- |
| `/new` | Start an empty conversation | `/clear` |
| `/sessions` | Switch to a project session | |
| `/resume` | Resume an interrupted run | `/continue` |
| `/providers` | Set up model providers | |
| `/models` | Select the model for new requests | `/model` |
| `/web-search` | Set up search providers | |
| `/trust` | Toggle project skill trust | |
| `/quit` | Exit the TUI | `/exit` |

Commands stay out of the conversation sent to the model. Opening and dismissing
the palette preserves your message or requested-input draft. The interrupt
shortcut still cancels the active run while a menu is open.

## Read output and scroll

User messages have a prominent left border. Assistant answers render Markdown,
including terminal hyperlinks when supported. Readable reasoning appears in
subdued blocks labelled Reasoning or Reasoning summary. Tool calls and results
also appear in the conversation.

Use the mouse wheel to scroll the conversation. The viewport follows incoming
output while you are at the bottom; scrolling up lets you inspect earlier
messages without being pulled back down. Menus receive wheel navigation while
open. The TUI uses the terminal's alternate screen and restores the prior screen
when it exits.

After a stream disconnects, Alfred reconnects and restores completed output.
Unfinished streamed text is provisional and can disappear. A reasoning-capable
model may provide no readable reasoning.

## Start a new session

Run `/new` or choose New session. Alfred clears the open conversation and any
unsent message or requested-input response. The previous conversation stays
available through `/sessions`, and work running there continues.

Opening an empty conversation does not create a session until you send a message.
Wait for any in-flight message, cancellation, trust change, approval, input, or
resume submission to finish before starting a new session.

## Switch sessions

Run `/sessions` or choose Switch session. Sessions for your project appear
newest-created first, with a title, creation time, and short ID. A session
without a title is labelled Untitled session; the open session is marked current.

Use Up/Down or Tab/Shift+Tab to choose, then Enter to open it. Esc closes the
picker without switching. Switching loads the conversation and follows its live
updates. It does not cancel the old session's work or resume the selected run.

Your ordinary message draft is kept across a switch. An unsent response to an
Alfred input request is silently discarded. Wait for pending submissions to
finish before switching. If listing fails, dismiss and reopen the picker to retry.

## Answer a question

When Alfred uses AskUser, it pauses and shows a question with a separate response
input. Enter your answer and press the submit shortcut, Enter by default.
Alfred resumes the same run using your response. Use the interrupt shortcut to
cancel instead.

SDK applications that install an approval policy can also ask for approval.
The TUI shows the requested action with `y` to approve and `n` to deny. The normal
application currently allows tool execution without per-call approval prompts.

## Project skill trust

When a project contains `.agents/skills` or `.claude/skills`, Alfred asks whether
to trust project skills. Use `t` to trust or `d` to deny; `/trust` toggles the saved
decision afterward. Global skill directories are independent of this choice.

This controls skill discovery, not tool permissions or instruction files.
Global and project-root `AGENTS.md` / `CLAUDE.md` loading remains independent.
Skill activation is not yet exposed as a model-callable operation.

## Cancel or resume work

Ctrl+C, by default, cancels the active run and keeps the TUI open. It discards
unfinished output; it does not undo completed tool effects. Use `/quit` to exit.
Quitting the client leaves the background service and its work running.

When a run is interrupted by a recoverable provider problem, resolve the cause
and run `/resume`. It keeps the same run and its original model. A plain message
such as “continue” creates a new run. Completed, failed, and cancelled runs cannot
be resumed through `/resume`.

Wait for the resume request to finish before switching sessions or submitting
another run action. If submission fails, Alfred displays the error so you can
retry. Service startup does not automatically resume unfinished runs.

## Customize keys

The defaults are Enter to submit, Ctrl+C to interrupt, and Ctrl+P for the palette.
See [Client settings](config.md#client-settings--clitoml) to change them in
`cli.toml`. Theme selection and appearance settings are not available yet.
