# Alfred repository guidance

Alfred is a personal agent with a Go core and a background service. The TUI is
the first client; web and native iOS clients will follow. Read
`docs/ARCHITECTURE.md` before making structural changes.

## Working agreement

- Work in small, reviewable steps. Implement only the concept requested in the
  current task.
- Do not generate broad scaffolding, speculative abstractions, or defensive
  boilerplate.
- Keep workflow edge-case handling minimal until a concrete problem requires
  it. Session switching silently discards unsent requested-input responses;
  do not add a draft guard or confirmation for them.
- The owner expects to account for every line. Prefer a small incomplete system
  with explicit seams over a large guessed implementation.
- Define domain types and interfaces one at a time and let implementations
  expose missing requirements.
- Do not add external dependencies unless the current step requires one and the
  choice has been discussed.
- Do not introduce a dependency-injection framework. Assemble dependencies
  explicitly in `backend/cmd/alfred/main.go`.
- Do not add compatibility layers before they are needed. The public contracts
  may change freely before v1 as the design is refined.
- Preserve unrelated user changes. Do not commit unless explicitly asked.
- Follow the frozen work order in `docs/ROADMAP.md`. Reorder it only at the
  owner's explicit request. Docs refinement, package consolidation, and CI,
  releases, and installation packaging precede the initial commit; TUI
  refinement follows it. Read that roadmap when choosing the next slice.
  This roadmap is intentionally local and gitignored; do not commit it or link
  to it from public documentation.
- Keep `.agentdesk/` ignored. It contains local handoff state and must not be
  included in `Initial commit`.
- The repository is a monorepo for the Go backend/core and future web and native
  Swift iOS clients. Keep
  language-specific source and generated artifacts in their own areas, retain
  language-neutral schemas and cross-stack documentation at the repository
  level, and update all path-sensitive tasks and documentation. Obtain owner
  approval before changing the Go module path.
- Before final composition, implement the local service transport, live event
  delivery, daemon discovery/process lifecycle, and the first functional TUI.
  These are part of the initial skeleton, not post-commit work.
- Before `Initial commit`, rewrite the architecture, state-machine, and related
  design documents as descriptions of the current system. Remove superseded
  choices and completed-work narration instead of preserving a historical log.
- Once the initial skeleton is wired in `backend/cmd/alfred/main.go` and the
  pre-commit roadmap stages are complete, review readiness with the owner before
  creating the repository's first commit with the message `Initial commit`.
- Propose each new code slice end to end for owner review before implementing
  it; implement each slice only after the owner asks to proceed. Start with the
  smallest useful settings and add features for concrete use cases. Requirements
  are archived in `.agentdesk/archive/config-design/requirements.md`; earlier code and drafts are
  archived under `.agentdesk/archive/config-reset-2026-10-29/`.
- Development conveniences belong in Taskfile targets or scripts that use normal
  application entry points. Do not add development modes or test-only execution
  paths to cmd, internal, or pkg. Initially support Linux and macOS only.
- CI uses Linux runners only. Cross-compile macOS binaries from Linux; do not
  add macOS runner jobs without a concrete need and the owner's approval.
  Keep CI streamlined and reuse the existing Taskfile checks.
- Complete the agreed application configuration layer before beginning
  operational observability.
- Defer the operational-observability layer until that configuration work is
  complete unless the owner explicitly reprioritizes it. Do not add piecemeal
  instrumentation or an OpenTelemetry dependency while completing the initial
  skeleton.

## Design preferences

- Optimize for conceptual clarity before extensibility. A design that technically
  supports middleware or replacement is not sufficient if its ownership is
  difficult to explain.
- Put behavior at the narrowest boundary that owns the relevant information.
  For example, model-call timing belongs around `ModelProvider.Stream`, tool
  approval belongs around tool execution, and active cancellation belongs in
  the dispatcher rather than the agent loop.
- Use functional options to install optional middleware on the subsystem it
  decorates. Do not make unrelated higher-level components retain middleware
  slices, nested copies of their own interface, or construction-only state.
- Structs should retain runtime state and dependencies that remain meaningful
  after construction. Keep temporary configuration in constructors.
- Pass dependencies directly to constructors. Introduce a `Dependencies` struct
  only when the configured argument-limit lint rule requires it.
- Treat configuration validation and defaulting as application responsibilities.
  Core constructors state what configuration they expect and may assume the
  composition root supplies it; do not duplicate required-value checks or
  defaults defensively in the core. Continue to validate domain inputs and
  data crossing runtime or persistence trust boundaries where correctness or
  safety requires it.
- Keep constructor settings in focused package-level `Config` structs and
  runtime collaborators in separate `Dependencies` structs. Constructor settings
  are internal inputs, not automatically the public file schema. The approved
  TOML decoder uses koanf tags on file-facing types; defaults are ordinary Go
  functions owned by the relevant module. Do not reintroduce default-value tags.
  Do not introduce an empty `Config` for a package that needs no settings.
- Keep file discovery and decoding in `internal/config`. The TUI owns client
  types, defaults, and validation; the config package must not import it.
  Server settings/defaults belong to `internal/config`. Apply saved theme
  overrides within the TUI; the config package must not access the database.
  File values are explicit, with
  no environment references. Only the server file path uses
  `ALFRED_CONFIG_FILE`; do not add a client configuration-path variable.
- Use XDG_CONFIG_HOME and XDG_DATA_HOME on both Linux and macOS, falling back
  to ~/.config and ~/.local/share. The public server setting is data_dir;
  do not reintroduce ALFRED_DATA_DIR.
- Resolve ConfigDir once in server defaults and reuse it for client loading and
  skill discovery. It is derived startup state, excluded from TOML decoding.
- Do not introduce indirection merely to make a pattern possible. If a wrapper
  makes a type appear to contain or delegate to itself, reconsider the layer.
- A mutex must correspond to genuinely shared mutable state. Localize it to the
  component that owns that state, and prefer durable conditional transitions
  for lifecycle correctness across processes.
- Telemetry is observational and must not become a correctness dependency.
  Persist separate semantic checkpoints when replay or state recovery requires
  them.
- Instrument only boundaries with a stated diagnostic purpose. Do not create a
  custom metrics, tracing, logging, export, or visualization framework; the
  post-initial-commit observability design uses established OpenTelemetry and Go
  facilities.
- Keep core metrics distinct from client-experience metrics. The core measures
  provider/model time to first event and first output; client-visible latency
  belongs in clients unless a future service boundary gives it core meaning.
- Prefer direct keyed lookup over repeated linear scans when identity-based
  resolution is part of the normal path.
- Before adding or changing an interface, check for duplicate responsibilities,
  redundant lookup methods, and types that the change would leave dangling.
- Use precise names that describe semantics rather than implementation
  accidents. Avoid parallel vocabulary for the same concept.
- When a design starts accumulating helper state, callbacks, mutexes, or adapter
  types, pause and re-evaluate the ownership boundary before adding more code.

## Go and package boundaries

- The Go module lives under `backend/`, remains
  `github.com/ArionMiles/alfred`, and targets Go 1.27.x. The owner explicitly
  chose to preserve the module and import paths through the monorepo migration.
  Root tasks are the stable developer interface for commands that must enter
  the nested module.
- The module-root `alfred` package is the stable domain and extension boundary.
  Its definitions begin in `backend/alfred.go` as requested by the owner.
- Keep the root package lightweight and directed inward: it must not import
  concrete providers, SQLite, JSONL storage, transports, Bubble Tea, or other
  client implementations.
- Concrete implementations belong in focused subpackages and depend on the
  root `alfred` contracts, never the reverse.
- Keep RPC/wire DTOs separate from domain types, even when their fields happen
  to match initially.
- Keep persistence schemas separate from domain types. A serialized domain
  value is not automatically a stable JSONL or SQLite contract.
- Treat JSONL schema versions, field names, units, and persisted enum values as
  durable contracts. Before changing one, present the compatibility or
  migration effect and obtain owner approval. Use stable named string enums
  directly when their constants are the durable vocabulary; do not use a
  human-readable `String` method as a persistence encoder.
- Put `context.Context` on cancellable operations rather than storing it in
  structs or passing it to constructors without a concrete need.
- Prefer narrow consumer-owned interfaces. Avoid umbrella interfaces that
  expose the entire harness.
- No-op implementations must satisfy the real contract honestly; they must not
  simulate behavior that has not been designed.

## Errors

- Use `github.com/ArionMiles/alfred/pkg/x/errors` throughout Alfred instead of
  importing the standard-library `errors` package directly.
- Construct and wrap application errors with `errors.B`. Use `Text` or `Textf`
  for internal diagnostic detail and `UserMsg` only for text safe and helpful
  to expose to a user.
- Classify missing resources with `errors.NotFound` / `KindNotFound()` and
  conflicting state with `errors.Conflict` / `KindConflict()`.
- Define subsystem-specific `errors.Kind` values at the boundary that returns
  them; `pkg/x/errors` provides a starting taxonomy, not an exhaustive registry.
- In every function or method that constructs or wraps an error, define `op`
  as that function or method's identifier with exactly the same spelling and
  case, and pass it to `errors.B.Op(op)`.

## Architectural invariants

- The harness owns global capabilities and infrastructure. Projects reference
  or constrain those capabilities; they do not own provider, tool, MCP, skill,
  scheduler, telemetry, or memory implementations.
- The core lifecycle vocabulary is: `Invocation` creates a `Run`; a `Run`
  belongs to a `Session`; a `Session` belongs to a `Project`; a `Run` emits
  ordered `RunEvent` values.
- `failed` runs are terminal. `interrupted` runs are resumable from durable
  history after a recoverable external condition is cleared. A plain
  `continue` message creates a new run; `/resume` and `/continue` are client
  control commands and do not become model input.
- `Turn` and `Step` are not core domain entities.
- All externally effectful tool calls cross one common execution boundary.
  Approval policy is replaceable middleware around that boundary.
- MCP is a tool discovery/invocation adapter. Once discovered, MCP tools enter
  the same registry and execution-policy path as built-in tools.
- Clients communicate with the background service through a shared contract.
  They do not read storage or invoke the agent loop directly.
- Local clients use the same HTTP RPC mode as future web and native clients;
  do not introduce a Unix-domain-socket-specific transport contract.
- The TUI may locate or start the service, but its lifecycle and presentation
  logic remain outside the core.
- Clients and invocation sources choose projects; workspaces and execution live
  on the daemon's filesystem. Managed workspaces belong under its data directory.
  Daemon selection and switching belong to client workflows. Do not add flags or
  connection-manager abstractions ahead of those workflows. A TUI opened inside
  the daemon container uses normal local discovery to connect to that instance.
- The TUI has separate configuration from the service. Centralize remappable
  actions, slash commands, palette entries, and help in one command/keymap
  registry; client control commands never become model input.
- TUI themes are data with light and dark palettes, selected through a theme
  name and light/dark/system color mode. Prefer maintained Bubble Tea ecosystem
  components before creating custom widgets.
- Use Bubble Tea's declarative terminal capabilities for alternate-screen,
  mouse, and OSC 7501 program-status behavior. Render assistant Markdown with
  Glamour and its OSC 8 hyperlinks; keep user input literal and visually
  distinct. Add a custom terminal escape utility only for a concrete protocol
  the maintained ecosystem does not expose.
- Source bundled TUI palettes from OpenCode's theme directory at
  `https://github.com/anomalyco/opencode/tree/dev/packages/ui/src/theme/themes`.
  Use it only as a convenient reference for established palette values and
  translate them into Alfred's own theme schema.
- JSONL is the authoritative transcript/event record. SQLite owns mutable
  application state and rebuildable transcript/telemetry projections.

## Change discipline

- Maintain user-facing guides in `docs/config.md`, `docs/providers.md`,
  `docs/tools.md`, and `docs/tui.md` alongside configuration, provider, tool,
  and TUI workflow changes.
  Keep `config.md` as a brief overview of each schema section with links to
  detailed workflow guides such as `providers.md`. Cover both TUI workflows and
  server `config.toml` and client `cli.toml` where relevant. Write from the user's
  perspective: explain how
  to accomplish a task and its visible result, not internal storage, interfaces,
  or implementation history. Start with skeletons and fill them in as features
  become usable; clearly label planned behavior and verify steps, examples,
  defaults, and links before presenting them as available functionality.
- Before editing an established contract, explain the semantic change and its
  effect on callers.
- Use embedded, method-named `.sql.tmpl` files for conditional SQLite queries.
  Templates receive only boolean flags; bind all data as named parameters.
- Update `docs/ARCHITECTURE.md` when an architectural decision changes. Keep it
  focused on rationale, tradeoffs, and constraints that code alone cannot explain.
  Do not maintain source maps, package inventories, API listings, or prose
  walkthroughs of implementation details. Code and tests are the reference for
  those details; duplicating them in documents creates drift and reading overhead.
- Update `docs/agent-state-machine.html` at meaningful lifecycle milestones,
  not after every individual state-machine change.
- Until `Initial commit`, add newly designed SQLite tables to migration
  `000001`; after that commit, treat applied migrations as immutable and add a
  new numbered migration for every schema change.
- Mark undecided behavior as undecided instead of filling gaps with assumptions.
- Comments should explain invariants or non-obvious decisions, not restate code.
- Add tests for behavior introduced in the current step; do not build a large
  hypothetical test matrix around no-op code.

## Validation

- Generate interface mocks into the root `mocks` package with `task mocks`.
  Use those generated mocks in unit tests instead of handwritten interface
  stubs. Storage integration tests exercise the real backend and do not mock
  database behavior.
- SQLite tests use a reusable test helper that creates a fresh database under
  `t.TempDir()`, opens it through the production constructor, applies the real
  migrations, and registers cleanup. Do not use mocks or Testcontainers for
  embedded SQLite.

For Go changes, run the smallest relevant checks and normally finish with:

```text
gofmt on changed Go files
task test
git diff --check
```

Report what was verified and any intentionally deferred work.
