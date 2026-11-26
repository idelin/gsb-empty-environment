// Package kt implements an importable multi-epoch key-transparency client:
// concurrent migration handling, append-only Merkle prefix consistency,
// resurrection (revived-key) blocking and dual-auditor fork detection.
//
// # Core objects
//
//   - BitPrefix: fixed-length bit strings up to 256 bits, used as labels and
//     tree prefixes.
//   - Tree: a fully expanded binary Merkle prefix tree bound to one epoch;
//     every node hash is domain-tagged and binds node type, height, bit
//     prefix and epoch number.
//   - SignedRoot / VerifyRootChain: a consecutive 1..8 epoch signed root chain
//     (Ed25519) with previous-root links and a monotonic revocation counter.
//   - Migration / MigrationLog / SignedAuth: per-epoch split, merge and
//     revoke-out operations, each requiring a fresh signed authorization.
//   - Verifier: thread-safe stateful client enforcing every rule below.
//   - ConsistencyProof / BuildConsistencyProof: non-adjacent-epoch append-only
//     consistency with minimal, uniquely reconstructable boundaries.
//   - AuditorView / ForkProof: dual independent-auditor comparison producing
//     independently verifiable fork fraud proofs.
//
// # Security properties enforced
//
//   - Revoked subtree hashes enter a graveyard; any later reuse (including
//     after split/merge relabeling) is rejected and hard-locks the verifier.
//   - A prefix that was once revoked can only reappear with a brand-new
//     authorization (unique nonce, strictly increasing per-issuer serial).
//   - Merge migrations prove sibling integrity: the declared sibling must be
//     the real bit-sibling and both children must recompute the parent hash.
//   - Migrations in one epoch must have pairwise disjoint prefixes.
//   - Consistency proofs reject redundant/overlapping boundary nodes and
//     incomplete label-space covers.
//   - Conflicting auditor roots or migration histories for one epoch produce
//     a ForkProof verifiable by any third party against the directory key.
package kt
