// Package service defines the abstraction that views consume to interact with
// git, the agent engine, the project loader, and Jira. It is the *only*
// backend-facing package the TUI is allowed to import (see MIGRATION-PLAN §2.4).
//
// Two implementations live in subpackages:
//
//   - service/local: calls internal/git, internal/engine, internal/project,
//     and internal/jira directly. Used by `singularityd` (the daemon) to
//     execute work in-process.
//
//   - service/remote: calls the HTTP+WebSocket SDK in internal/client. Used
//     by the `singularity` TUI; every method becomes an RPC to the daemon.
//
// Type identity is preserved across the boundary by re-exporting (via type
// aliases) the data DTOs that already live in internal/git, internal/engine,
// internal/project, and internal/jira. JSON tags on those types are the
// canonical wire format. Operations are the boundary; types are shared.
//
// All methods accept a context.Context as their first argument so that
// cancellation propagates across the wire (HTTP request cancel → daemon-side
// context cancel). Streaming/long-running methods return a receive-only
// channel of events plus a cancellation closure; closing the channel signals
// the stream is done, calling the closure unsubscribes early.
//
// Sentinel errors crossing the boundary are declared in this package (see
// errors.go). The remote implementation MUST map server-sent error codes
// back to these sentinels so that view code can use errors.Is uniformly.
//
// Constructors live in the implementation subpackages. This package only
// declares the contract.
package service
