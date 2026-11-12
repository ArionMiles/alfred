# Model providers

Use `/providers` or Ctrl+P → Providers to save model-provider settings. Use
`/models` to choose the model Alfred will use for new requests. Search backends
such as Exa have a separate [Web search setup](tools.md#set-up-web-search) flow.

## Available providers

| Provider | Setup | Model use |
| --- | --- | --- |
| OpenAI | API key, optional Base URL; Continue with ChatGPT | Responses API through an API key or ChatGPT plan |
| DeepSeek | API key, optional Base URL | Not available |
| Fireworks AI | API key, optional Base URL | Not available |
| Amazon Bedrock | Region, optional profile and endpoint | Not available |

The TUI stores one setup per built-in provider. Multiple provider setups can
coexist, but only OpenAI currently supplies models. An OpenAI setup uses either
an API key or one ChatGPT account; saving one method replaces the other.
Disabling/removing connections and multiple accounts for one provider are not
available yet.

## Connect a provider

1. Open `/providers` or choose Providers from the palette.
2. Use Up/Down or Tab/Shift+Tab to select a provider, then press Enter.
3. Choose its setup method, fill the form, and select Save.

Fields marked * are required. API-key inputs are masked. Use Tab and Shift+Tab
to move between fields, Save, and Cancel. Enter advances from a field; on Save
it submits, and on Cancel it closes without saving. For choice fields, use
Up/Down to select an option. The mouse wheel scrolls long forms, and moving focus
brings the selected field into view.

Validation errors appear beside the affected fields. Correct them and select
Save again. A failed save retains your inputs for retry. A successful save clears
secret inputs and shows Settings saved; press Enter or Esc to return.

Esc or the palette shortcut dismisses unfinished setup without saving. Once
Save is submitted, wait for its result before closing. Entering and leaving
through the palette preserves your conversation draft.

Saving a setup form does not verify credentials or account access. Browser
sign-in validates the returned account and permissions. Open `/models` after
connecting OpenAI to select a model before sending your first message.

## OpenAI

Choose OpenAI, then choose one of its setup methods.

For an API key, enter the key and leave Base URL blank to use the normal OpenAI
endpoint. To use another endpoint, enter its Base URL in this form. That endpoint
must support the Responses API; Chat Completions-only endpoints are not supported.

Select Save, then open `/models`. To replace a key later, reopen the same method.
Saved non-secret values are prefilled; the saved key is never displayed and must
be reentered when saving. Endpoint settings are edited through this form, not
through provider entries in `config.toml`.

### Continue with ChatGPT

Choose Continue with ChatGPT to connect a ChatGPT subscription. Alfred opens the
system browser and waits for OpenAI to return to a temporary loopback address on
`127.0.0.1`. If the browser does not open, copy the displayed URL into a browser
on the same computer. Complete the OpenAI account and permission screens, then
return to Alfred. Press Esc while Alfred is waiting to cancel without replacing
the saved connection.

Browser sign-in expires after ten minutes by default. If an attempt expires,
start Continue with ChatGPT again. To allow more time, set this in the server's
`config.toml` before starting the daemon:

```toml
[providers.openai.authorization]
attempt_timeout = "15m"
```

Alfred validates the returned account identity before replacing the connection.
The browser flow uses a stable, randomly generated identifier for this Alfred
data directory; it does not derive the identifier from the host name or hardware.
Tokens are stored through Alfred's credential storage and are not shown in the
TUI. Alfred renews expiring access automatically and atomically saves OpenAI's
replacement refresh token. If renewal is rejected, reconnect the account from
Providers.

Open `/models` after connecting. Alfred asks OpenAI which models are visible to
that ChatGPT account and shows only those it also knows how to use. Requests use
the account authorization, do not retain response state at OpenAI, and send the
full conversation history needed for each run. If the account identity is valid
but ChatGPT plan usage was not granted, Alfred keeps the connection and asks you
to reconnect before models can be used.

The loopback callback means this flow currently supports only a TUI and daemon
running on the same host; remote and container callback workflows are planned.

### Restart and reconnect

Completed connections survive quitting the TUI and restarting the daemon. Open
`/models` to select a model, or keep using your saved selection. Expiring access
renews automatically; there is no need to repeat browser sign-in after a restart.

If the daemon restarts during an unfinished browser sign-in, that attempt cannot
be completed. Close its browser tab. If Alfred cannot reach the daemon, press Esc
to close the authorization screen, then quit and reopen Alfred to discover the
restarted service. Open `/providers` → OpenAI → Continue with ChatGPT to start again.

When access expires or is revoked, reopen the same Continue with ChatGPT method.
Choose the original account and workspace. Alfred reuses the saved registration;
a denied, canceled, failed, or mismatched-account authorization leaves the saved
connection untouched. Signing in to a different account is not supported yet.
If you previously declined plan usage, reconnecting asks for permission again.
You can instead explicitly save an API key, which replaces the subscription setup.

### Eligibility and usage limits

ChatGPT plan access depends on the selected account, workspace, policy, and the
capabilities OpenAI permits. An eligibility error requires checking those
restrictions; repeating sign-in is not a way around them. An unsupported-request
error requires changing the request or model before trying again.

For usage-limit errors, check [ChatGPT Settings → Usage](https://chatgpt.com/settings/usage).
Both app-specific and plan limits can apply. A usage-limit error does not tell
Alfred when a limit resets or mean that the entire plan is exhausted. Other apps
using the same ChatGPT plan can affect its available usage. OpenAI describes the
current rules in [Accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions).

Temporary account-availability or network failures keep the saved connection.
Wait before trying again. Alfred does not automatically switch to API-key billing.
A failure after text starts streaming is still a failed model response; partial
text is not committed as a completed answer and its tool calls are not executed.
Use `/resume` only if Alfred reports an interrupted run; a failed run needs a new
request once the cause has been addressed. See OpenAI's
[Errors and recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery)
for the provider's recovery rules.

### SDK authorization presentation

Applications composing the Go SDK can customize authorization presentation:

Initialize the public endpoint defaults with `github.com/creasty/defaults`
before applying any application-specific overrides:

```go
var settings openai.AuthorizationConfig
if err := defaults.Set(&settings); err != nil {
    return err
}
authorization := openai.NewAuthorizer(
    settings, dependencies,
    openai.WithAgentName("My Agent"),
    openai.WithSuccessHTML("<!doctype html><title>Connected</title><h1>Ready</h1>"),
)
```

`dependencies` supplies the application's stores. Register the authorizer with
`providers.WithAuthorizer("openai", authorization)` when constructing the setup
service. Its `Shutdown(ctx)` cancels active attempts and waits for cleanup before
you close those stores. For direct SDK use, `Authorize` runs one attempt under
the supplied context and returns only after its callback handlers stop.
`settings.AttemptTimeout` defaults to ten minutes; cancellation can end it sooner.

The authorizer builds its HTTP client using `settings.RequestTimeout`. Use `WithAuthorizationHTTPClient` for a
custom authorization transport; the supplied client's own timeout applies.
These constructor options are separate from server TOML and TUI settings.
The agent name is used for first registration and the bundled callback page;
it does not rename a registration already issued by OpenAI. Custom success HTML
is served verbatim only after credentials and plan permission have been validated
and saved. It is not a callback handler or template and receives no tokens.
Declined permission and failed authorization retain Alfred's own recovery messages.
Omit `WithSuccessHTML`, or supply an empty string, to use the bundled page.

The default page is embedded from
`backend/pkg/providers/openai/authorization.html`; it supports light and dark
appearance without external resources.

### Reasoning output

When the model supplies readable reasoning, Alfred shows labelled Reasoning
summary or Reasoning blocks separately from the answer. Text appears as it
arrives; completed blocks remain when you reopen the session. A model may
provide no readable reasoning. Effort selection is not available.

Cancellation discards unfinished output. On a stream disconnect, Alfred restores
completed output when it reconnects. See [Terminal client](tui.md) for session
navigation, cancellation, and resumption.

## Choose a model

Run `/models` (also `/model`) or choose Models from the palette. Rows show model
names with quieter provider names on the right. A checkmark marks the saved
selection.

Use Up/Down, Tab/Shift+Tab, or the mouse wheel to navigate. Enter saves the
highlighted model and closes the picker. Press `/` to search model/provider
names, then Enter to finish filtering and Enter again to select. Esc clears a
filter; with no filter, it closes the picker.

Alfred remembers the selection across projects, sessions, and restarts. It
applies to new requests; existing and resumed runs keep their original model.
Before the first selection, sending a message asks you to choose one.

For API-key setup, the catalog describes published models rather than your
account's entitlements, so a listed model may still reject a request. ChatGPT
plan setup intersects that catalog with the models OpenAI reports for the signed
in account. If no models appear, reconnect OpenAI and reopen Models. An
unavailable saved selection is not silently replaced: choose another. After
loading fails, dismiss and reopen to retry; a failed save leaves the choices
available. Opening through the palette keeps your message or requested-input
draft.

## Other setup forms

DeepSeek and Fireworks AI accept an API key and optional Base URL. Reopening a
saved form preserves endpoint settings but requires reentry of the key. These
forms save settings; their models are not usable through Alfred yet.

Amazon Bedrock accepts an AWS region, optional AWS profile, and optional custom
endpoint. Reopen its form to edit the saved values. No Bedrock entry in TOML is
needed. Saving does not access the AWS credential chain or verify model access;
Bedrock inference is not implemented.

## Troubleshooting

If setup loading fails, dismiss and reopen Providers. If a field is invalid,
correct it and save again. If saving fails, your form remains available for
retry. The saved API key cannot be viewed; reenter it to replace the setup.

For unavailable models or account-access failures, confirm the OpenAI setup and
selected model. Provider errors can interrupt or fail a run; `/resume` applies
only when Alfred reports an interrupted run. A failed run requires a new request
after the underlying problem is corrected.
