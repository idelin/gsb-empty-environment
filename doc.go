// Package netsec implements client-side consistency checking for account
// device lists distributed by an untrusted message directory.
//
// A device list is an append-only, sorted AccountState. Every state is signed
// with the account's Ed25519 master key over a deterministic canonical
// encoding that includes the account ID, version, complete device set, and
// previous-list digest. Clients pin the highest accepted version and digest,
// require exact version succession, and require the previous digest to match
// the saved chain head.
//
// A signed list is committed only after a transparency-log inclusion proof
// places its digest in the checkpointed log. The host injects a ProofVerifier
// for checkpoint signatures and witness policy. A list staged without a proof
// is not authorized: new devices cannot receive messages and the session layer
// does not expose them. Committed removals are checked dynamically by every
// session, so an existing session immediately stops new sends.
//
// Contacts exchange SignedObservation receipts. A same-version digest
// disagreement, or two different list digests anchored to the same checkpoint,
// freezes new sessions and produces portable ForkEvidence. Consistent older
// receipts are recorded as stale rather than treated as forks.
package netsec
