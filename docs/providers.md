# Model providers

Use `/providers` or Ctrl+P → Providers to save model-provider settings. Use
`/models` to choose the model Alfred will use for new requests. Search backends
such as Exa have a separate [Web search setup](tools.md#set-up-web-search) flow.

## Available providers

| Provider | Setup | Model use |
| --- | --- | --- |
| OpenAI | API key, optional Base URL | Responses API |
| DeepSeek | API key, optional Base URL | Not available |
| Fireworks AI | API key, optional Base URL | Not available |
| Amazon Bedrock | Region, optional profile and endpoint | Not available |

The TUI stores one setup per built-in provider. Multiple provider setups can
coexist, but only OpenAI currently supplies models. Subscription sign-in,
authentication checks, disabling/removing connections, and multiple accounts
for one provider are not available.

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

Saving settings does not verify credentials or account access. Open `/models`
after saving OpenAI to select a model before sending your first message.

## OpenAI

Choose OpenAI, then API key. Enter the key and leave Base URL blank to use the
normal OpenAI endpoint. To use another endpoint, enter its Base URL in this
form. That endpoint must support the Responses API; Chat Completions-only
endpoints are not supported.

Select Save, then open `/models`. To replace a key later, reopen the same method.
Saved non-secret values are prefilled; the saved key is never displayed and must
be reentered when saving. Endpoint settings are edited through this form, not
through provider entries in `config.toml`.

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

The catalog describes published models, not your account's entitlements. A
listed model may still reject an inference request. If no models appear, connect
OpenAI and reopen Models. An unavailable saved selection is not silently
replaced: choose another. After loading fails, dismiss and reopen to retry;
a failed save leaves the choices available. Opening through the palette keeps
your message or requested-input draft.

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
