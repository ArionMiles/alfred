# Alfred architecture

This document records the reasons behind Alfred's architectural boundaries and
the constraints future changes should preserve. Read the code and tests for
implementation details and current APIs. User workflows belong in the
[guides](../README.md#guides).

## A reusable core, composed into an application

Alfred is intended to serve both SDK users and people running the application.
An SDK user should be able to replace a model integration, credential backend,
execution policy, or storage implementation without adopting the daemon or TUI.

The domain contracts therefore stand independently of concrete integrations.
Dependencies point toward those contracts. The application assembles concrete
implementations explicitly rather than introducing a service locator or plugin
framework. This makes composition more verbose, but keeps dependency ownership
visible and avoids making application startup conventions part of the SDK.

Extensibility does not require every consumer interface to become public. A
private, narrow interface can be satisfied by an SDK user's implementation.
Shared contracts are useful when they establish common semantics, rather than
merely collecting methods that happen to be used together.

Components generate opaque identities internally using cryptographic randomness.
Identity generation is an implementation detail rather than an SDK setup option;
tests verify persisted identity relationships instead of requiring fixed values.

Core operations receive creation timestamps from their callers rather than
depending on a shared clock abstraction. Time-dependent decisions accept an
explicit instant so their boundary behavior can be tested directly. Infrastructure
that observes multiple moments, such as streaming measurements and network token
renewal, reads current time at each observation rather than reusing an earlier
timestamp for the whole operation.

Application startup resolves one aggregate configuration before composition.
Static defaults live on the owning settings structs and are applied before
file values and explicitly named environment overrides. Loading resolves the
application's configuration and data directories; composition supplies component
directories and derives database, history, and skill paths. The model catalog
owns its cache filename and resolves it within the supplied cache directory.
The aggregate does not retain copies of derived paths. Configuration types own
their validation rules;
application loading calls their `Valid` methods before path normalization can
hide invalid input. Constructors receive concrete settings without reading
process configuration, so SDK users can validate and compose them independently.

The aggregate is not the public file schema: internal tuning and integration
endpoints remain constructor settings. Environment exposure is explicit rather
than inferred from every field. Persisted provider connections and credentials
retain their own runtime workflows and do not join the startup source precedence.

Components own endpoint, request-timeout, and cache-filename defaults and build
their HTTP clients from those settings. Optional client overrides support SDK
transport customization without making routine client construction part of
composition. Overrides preserve component security policies, including Exa's
redirect restriction. Search, page fetching, catalog refresh, and provider
authorization have independent request timeouts; streamed inference defaults to
no total request timeout.

## Execution belongs to the service

A conversation should survive closing a terminal or reconnecting through a
different client. Clients therefore submit work and observe it through a service
contract; their connections do not own execution lifetime. Waiting for a person's
answer or approval must also survive the original request ending.

This decision introduces daemon discovery and client synchronization that an
in-process terminal application could avoid. The shared HTTP contract accepts
that cost to keep terminal presentation independent of execution and avoid a
second transport model for other clients.

The deployment assumption is one service per data directory in a trusted
environment. Daemon selection belongs to clients; the RPC boundary allows that
workflow to evolve without changing agent execution. Local discovery and process
startup are client behavior. Process ownership and stale-service detection do not
establish authentication or an OS security boundary. Access from untrusted
networks requires a separate security design.

## Capabilities belong to the harness

Projects identify workspaces and group conversations. They can constrain shared
capabilities, but should not own independent implementations of providers,
credentials, tools, or execution infrastructure. Keeping those capabilities at
the harness level avoids duplicating integration setup for every project.

Model inference, model onboarding, and web search remain distinct capabilities.
A common API shape or shared credential storage does not give them the same
lifecycle or ownership. Adding an inference adapter should not force it to adopt
the default application's onboarding UI.

Provider onboarding metadata belongs to the service. Clients select a declared
provider and setup method, submit only the method's input values, and render the
returned state. Provider definitions and implementations own configured-instance
identity, display names, and defaults so different clients cannot create
different meanings for the same setup method.

Configured inference resolution belongs to the concrete provider, which owns
its credential formats, supported settings, and authentication methods. The
registry delegates through the provider resolver contract and applies shared
middleware. Catalog loading and caching remain provider-neutral; a concrete
resolver decides which published metadata it requires. Composition connects
these boundaries without making the catalog depend on an installed adapter.

OAuth callback presentation is a composition choice owned by the authorization
service. SDK applications can supply static success HTML and their agent name;
clients and file configuration do not interpret those choices. Custom HTML is
served only after validated credentials and plan permission are saved, so
presentation cannot establish authorization success or hide a declined grant.

## Durable history and mutable state have different jobs

JSONL records ordered execution facts; SQLite owns mutable application state.
This separates replayable history from the queries and conditional updates
needed to operate the application. It also lets derived views evolve without
redefining the underlying conversation record.

The cost is independent commit boundaries. A state transition and its history
event do not share a transaction. An external tool effect and its recorded
result are also separate: a crash between them can leave an effect that replay
cannot recognize as completed. Durable checkpoints alone cannot promise
exactly-once execution.

Any new persisted fact needs a clear authority and an explicit account of what
happens between commits. Domain values, persisted representations, and client
messages have different compatibility and visibility requirements; similar
fields are insufficient reason to merge those contracts.

## Presentation is not evidence of completion

Streaming makes the client responsive, but partial output is insufficient for
execution decisions. Complete model output must be committed before its tool
calls are executed. A delta, an announced tool call, or a usage measurement
cannot establish that the model request succeeded.

This distinction lets clients lose provisional updates and reconcile from
history without changing what the agent executes. It costs additional replay
and reconciliation logic, but keeps correctness independent of token delivery.
Measurements serve observation; semantic checkpoints establish execution
progress. Telemetry failures should not decide whether work can proceed.

## Provider neutrality must preserve information

The agent loop should reason about messages, reasoning, tool calls, and results,
rather than a particular provider's wire protocol. However, a provider can
require opaque data from an earlier response to continue correctly. Removing
that data would make an otherwise neutral abstraction unusable for that
provider.

Native data therefore stays attached to the semantic item it belongs to, with
interpretation owned by the adapter. Tools and clients should not need to
understand it. This preserves protocol information at the cost of storage
compatibility obligations and limits on replay through a different protocol;
it does not imply automatic cross-provider translation.

Some providers can continue from an opaque response or conversation handle
instead of receiving the complete history again. Adapters may use that facility
to reduce latency and time to first output, but Alfred's durable history remains
authoritative. A continuation handle is valid only for the provider capability
and compatible history prefix, provider instance, model, and credential that
produced it. Switching providers, changing an incompatible prefix, losing the
remote state, or encountering an unsupported request mode falls back to normal
history replay. Provider-managed retention is therefore an optimization, not a
session-liveness dependency.

## Tool effects share an execution boundary

Authorization belongs at the common tool execution boundary so that adding a
tool does not create another approval mechanism. The runner owns sequencing and
resumption; policy owns authorization; the tool owns its operation. Keeping
those responsibilities separate allows SDK users to replace policy without
rewriting tools.

These are application boundaries between trusted components. They do not confine
arbitrary Go code or a shell process. A clean shell environment reduces accidental
inheritance of server secrets, but filesystem and network isolation require a
separate security mechanism.
