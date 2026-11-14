// Package sessiontakeover provides an importable, broker-agnostic MQTT
// session-takeover guard.
//
// MQTT persistent sessions (clean start = false) survive disconnects:
// subscriptions, the last will, unacknowledged QoS 1/2 packets and the
// offline queue must all be replayed to the next connection that presents the
// same client ID. Replaying that state to the wrong authenticated subject is
// a cross-credential data leak. This package models a session by
// (tenant, client ID, subject) plus a monotonically increasing connection
// generation and enforces three invariants:
//
//   - A session is migrated (resumed) only when the new connection proves the
//     same tenant and authenticated subject and its credential is not
//     revoked. Every CONNECT is re-checked through CredentialInspector.
//   - A different subject using the same client ID is either rejected
//     (ConflictReject) or placed in its own isolated subject-scoped session
//     (ConflictIsolate). The original session is never silently overwritten.
//   - Sessions are tenant-scoped and expiry-scoped: a client ID in another
//     tenant, or an expired session, never migrates.
//
// On a successful resume the will, subscriptions, inbound QoS 2 replay
// ledger, unacknowledged outbound packets and offline queue all stay attached
// to the same Session object, while the previous connection's Handle is
// atomically invalidated; any further use of the old Handle fails with
// ErrHandleClosed so displaced connections cannot touch migrated state.
//
// The package depends only on the Go standard library and is safe for
// concurrent use.
package sessiontakeover
