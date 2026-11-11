# Offline transparent-log checkpoint receiver

This module implements a persistent, crash-safe checkpoint acceptor for a Go
software-update client. The client must keep trusting a transparent
append-only log even when it is offline, so every verified tree head it has
ever accepted is remembered on local disk.

It depends only on the Go standard library (Ed25519 + SHA-256), so it builds
and verifies fully offline.

## Packages

- `internal/tlog` — Merkle tree math ported from
  `golang.org/x/mod/sumdb/tlog` / RFC 6962: leaf and node hashing, tree roots,
  and Merkle consistency-proof generation and verification over an in-memory
  leaf slice.
- `internal/checkpoint` — signed tree heads. A checkpoint binds an origin, a
  tree size and a root hash and authenticates them with an Ed25519 signature
  over the canonical header text.
- `internal/receiver` — the durable acceptor. It verifies signatures and
  consistency proofs, then atomically advances the on-disk root.

## Acceptance rules

`Receiver.Observe(signedNote, proof)` applies these rules while holding an
in-process mutex and an advisory file lock:

1. **Signature first.** The checkpoint must verify against the pinned Ed25519
   key and the pinned origin. Nothing is trusted before this check.
2. **First head.** The first valid signed head is accepted and persisted.
3. **Stale head.** A smaller tree size than the accepted head is rejected with
   `ErrStale`. The stored root never moves backwards.
4. **Consistent extension.** A larger tree is accepted only when its Merkle
   consistency proof verifies against the current root.
5. **Equivocation.** Two different roots at the same tree size freeze the
   receiver permanently (`ErrFrozen`). Both signed checkpoints are written to a
   `conflict-<size>.json` evidence file, and even valid later updates are
   refused. The freeze survives restarts.
6. **Idempotent.** Re-delivering the exact current head is a no-op.

`Receiver.ObserveFrom(mirror)` wraps a `Fetcher` (a possibly malicious mirror)
and is the entry point download threads call.

## Durability and concurrency

- State is committed as `checkpoint-state.json` via the crash-safe sequence
  write-temp → `fsync(temp)` → rename → `fsync(dir)`. A crash before the rename
  leaves only a temp file, which is cleaned up on the next `Open`; the previous
  state stays authoritative.
- The stored JSON embeds the original signed note and is re-verified on every
  read, so a garbled or tampered file fails closed rather than allowing a
  rollback/re-bootstrap.
- An in-process mutex serializes threads inside one process; a per-directory
  channel gate plus `flock(2)` serialize separate processes and avoid
  self-deadlock when the same process reopens a directory.

## Running the tests

```sh
go test ./...
go test -race ./...
```

The suite covers:

- consistency proofs across many non-power-of-two tree sizes (exhaustive
  generator/verifier pairings in `internal/tlog`, plus irregular receiver
  sequences such as 1, 2, 3, 5, 6, 7, 9, 13, 17, 20);
- stale mirrors that cannot roll back an accepted root;
- same-size different-root equivocation, freezing and conflict evidence;
- simulated power loss before and after the atomic rename, temp-file cleanup,
  corrupt and tampered state failing closed, and cross-origin rejection;
- many concurrent download threads mixing honest and stale mirrors, asserting
  the head only ever moves forward;
- a cross-process `flock` test using a re-executed test binary.
