// Package capacitycred implements importable, multi-tenant KDF memory capacity
// credentials for cross-node relay processing.
//
// Credentials form a quota chain: a mint creates a root with a total byte
// quota and an input-bound parameter commitment; signed split events partition
// that quota into disjoint, numbered intervals; signed handoff events move a
// credential subtree between nodes; signed revoke events block future
// reservations. The component only invokes host-injected hash and signature
// verification callbacks and rejects missing or nil callbacks at construction
// and at every receive entry instead of degrading to skipped verification.
//
// Online ingestion accepts causal events in any order: unresolved events stay
// buffered until their parent credential and predecessor event arrive.
// Competing transfers of an overlapping interval are detected symmetrically,
// so both branches are quarantined even when both signatures verify, while
// disjoint sibling intervals remain allocatable. Recompute replays the
// immutable event set and produces the same Snapshot as online merging.
//
// A local node may only allocate after the full quota chain is present and a
// caller finality receipt binds the credential root to the finalized event
// prefix. Reservations measure memory exactly as s_cost*32 with checked
// 64-bit arithmetic. Each task id reserves at most once; re-entry after a
// crash with the same request is idempotent. Buffer erasure always precedes
// the single quota release, including callback error, panic and cancellation
// paths.
package capacitycred
