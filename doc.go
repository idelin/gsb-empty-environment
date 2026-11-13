// Package netsec implements an importable anti-double-sign ledger for
// threshold-signing shares.
//
// A fixed committee member signs a Share covering the chain ID, member ID,
// height, round, phase, block digest, and parent digest with Ed25519. The
// ledger enforces per-member safety without trusting arrival order:
//
//   - At one height and phase, a member may support only one block.
//   - Prepare and commit at the same height are independent phases.
//   - A commit needs a matching prepare at the same height/block with a
//     round no higher than the commit round; rounds never regress at a height.
//   - A prepare at height h must extend the member's highest committed block
//     below h. Intermediate heights may be absent.
//   - Shares whose prerequisites are missing are stashed and published once
//     the prerequisites arrive.
//   - Two authentic conflicting shares produce minimal, independently
//     verifiable DoubleSignEvidence and freeze the member permanently.
//
// NewLedger is in-memory. OpenLedger adds per-member write-ahead logs and
// atomic snapshots so restart, duplicate delivery, and a crash in the middle
// of a batch cannot lose published shares, pending shares, or evidence.
package netsec
