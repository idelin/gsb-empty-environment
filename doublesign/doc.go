// Package doublesign is an importable anti-double-signing audit and slashing
// component for BFT threshold validators that rotate their validator set
// across epochs.
//
// It maintains epoch validator sets anchored by a signed root (fixed member
// IDs, Ed25519 public keys and voting weights), validates every consensus
// share against the safety rules, stages out-of-order shares, permanently
// freezes convicted members, persists all state for crash recovery, and emits
// minimal self-contained convictions that any third party can verify offline
// with only public keys.
//
// The four safety rules enforced are:
//
//   - Equivocation: a member must not sign two different block digests at the
//     same (epoch, height, round, phase).
//   - Commit-before-prepare: a commit requires the member's own valid prepare
//     for the same height/block at round <= commit round.
//   - LockedQC no-rollback: after acknowledging a higher lock at a height, the
//     member cannot move to a higher round supporting a conflicting block with
//     a lower lock.
//   - Parent linkage: every prepare's parentDigest must extend the member's
//     highest committed block.
package doublesign
