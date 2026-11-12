// Package bufauth centralizes authorization for graphics buffer handoffs.
//
// # Threat model
//
// A buffer proxy passes buffer file descriptors to peer processes over Unix
// domain sockets (SCM_RIGHTS). Two attacks are in scope:
//
//   - A holder of a read-only grant asks the proxy to deliver the buffer as
//     writable, or to gain reexport/mapping rights it never received.
//   - A holder keeps using a buffer, or leaves a handoff in flight, after the
//     owner has revoked access.
//
// # Derived grants
//
// Authority.Issue creates a root grant. Every Authority.Forward call derives
// a child grant whose permission mask must be a subset of the live parent
// mask; the parent must also hold PermReexport. The tree structure makes the
// provenance of every grant explicit.
//
// # Cascade revocation
//
// Authority.Revoke flips the target grant and all descendants and reclaims
// every pending transfer reachable from the revoked subtree. Reclaim closes
// the authority-owned duplicate of the file descriptor. A descriptor already
// queued in a socket buffer cannot be removed from the kernel, so its token
// is invalidated and Authority.Claim rejects it when the receiver presents
// the token, preventing use of a revoked buffer.
package bufauth
