// Package ptrauth provides an authenticated pointer wrapper intended to be
// called by a memory-safe runtime. Every pointer capability carries an HMAC
// tag binding five attributes:
//
//   - the target address,
//   - the object generation (invalidated on free and address reuse),
//   - the access kind: read-only, read-write or executable (W^X enforced),
//   - the call context in which the pointer was authenticated,
//   - the authorized byte span.
//
// Security guarantees:
//
//   - Derivation (Derive/Subslice) recomputes the tag for the new address
//     and span and only permits kind preservation or narrowing; a data
//     pointer can never be relabeled as a function pointer.
//   - Free retires the generation atomically and re-registering the same
//     address advances the generation, so tags from a previous occupant are
//     rejected as stale.
//   - Every dereference (Read/Write/Call) verifies the HMAC, the call
//     context and the live object generation under the object lock before
//     touching memory. A concurrent free either completes before the gate
//     (retired generation) or waits for the access to drain.
//   - Cross-context borrowing requires Recontext, which verifies the source
//     context and mints a fresh tag. Swapping context fields by hand fails
//     verification and is never downgraded through the gate.
//
// The Authenticator is injected so tests can exercise the full policy with
// real HMAC-SHA256 material while production runtimes own key management.
package ptrauth
