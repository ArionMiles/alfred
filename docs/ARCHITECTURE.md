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
