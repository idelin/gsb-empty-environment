// Package netsec implements the device-admission and scoped-sync policy
// component used by a Go credential-vault service.
//
// The package deliberately separates three capabilities:
//
//   - Admission: a new device starts as pending. An already approved device
//     must explicitly approve it and supply the account key wrapped to the
//     new device's X25519 key.
//   - Recovery: recovery codes are high-entropy, stored only as keyed
//     digests, consumed atomically, and generate an alert when used. A
//     recovery code only creates a pending request; it never grants access
//     by itself.
//   - Sync: an approved, non-revoked device may read only entries that were
//     explicitly granted to it. Account membership is checked before any
//     entry lookup, which prevents cross-account access.
//
// Revocation changes the device state under the service lock. Every
// subsequent authorization check observes the revoked state, so sync stops
// immediately after RevokeDevice returns.
//
// The hosting vault service is responsible for authenticating the
// transport-level identity of a calling device before passing its DeviceID
// to this component. Account keys and entry payloads are treated as opaque
// application data; the service stores only ciphertext and wrapped key
// envelopes.
package netsec
