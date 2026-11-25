# Authenticated Name-Cache Patch Verification

## Packages

- `zoneauth` — the importable authenticated patch verification component.
  - `names.go`, `records.go`: label-structured names and record sets.
  - `trie.go`: committed trie node with deterministic canonical hashing.
  - `envelope.go`: real Ed25519 root and key-handoff signatures.
  - `proof.go`: partial proof structure, structural verification and answer
    derivation (positive / referral / nodata / nxdomain).
  - `patch.go`: finite operations (`add_delegation`, `remove_delegation`,
    `set_wildcard`), optional handoff, and `VerifyPatch`.
  - `client.go`: `Reevaluate` / `ReevaluateVerified`, producing
    `reusable` / `changed` / `pending` verdicts.
  - `zone.go`, `builder.go`: manager-side full tree, snapshotting and signed
    patch construction.
- `zoneinterp` — a fully self-contained full-zone-tree interpreter. It never
  imports `zoneauth`; cross-package parity tests require both implementations
  to reach identical answers over the same logical trees.
- `testsupport` — fixed-seed Ed25519 key material and the four deterministic
  golden fixtures under `testdata/`.
- `cmd/gen-golden` — regenerates the byte-stable golden fixtures.

## Trust model

1. Every trie node has a canonical SHA-256 image covering its path, flag,
   ordered records and the full `label -> child hash` map. A committed node
   therefore authenticates both child presence and child absence.
2. A root envelope signs `(zone, sequence, key id, previous key id, root
   hash)` with Ed25519. Sequence must advance by exactly one.
3. A handoff envelope is signed by the OLD key and binds the old root hash,
   the new root hash and the new public key. The new root envelope is then
   signed by the new key and records the previous key id, so the trust chain
   extends without any out-of-band channel.
4. A patch carries the new committed apex node (`new_apex`) plus per-operation
   old-root and new-root partial proofs. Optional `client_proofs` sign out
   minimum new-root evidence for specific cached queries.

## Evidence rules

- An addition must prove the target absent under the old root (via the
  explicit parent node's children map) and present as a delegation at the
  identical path under the new root.
- A removal must prove the deleted delegation existed under the old root and
  that the same path is a regular node under the new root.
- A wildcard change is anchored at the parent; both a real `*` chain node and
  an anchor-only missing-edge shape are accepted, provided every edge is
  hash-bound.
- An unprovided sibling is never treated as an empty tree. When a committed
  edge exists but its node evidence is not held, the client returns
  `pending` rather than fabricating `nxdomain`.
- Old-root nodes are adopted under the new root only when their hash matches
  the edge a new-root parent commits; modified subtrees cannot be smuggled in.
- All ancestry is label structural. `kee` and `keep` are unrelated names;
  changes propagate from the closest existing ancestor through that
  ancestor's subtree only.
- A wildcard template covers exactly one label beneath its anchor.

## Fixed fixtures

The golden files use fixed Ed25519 seeds, so signatures are real yet
deterministic. They cover:

- `handoff` — cross-zone key handoff combined with a new delegation.
- `wildcard-only` — only the wildcard template changes; the root moves but
  the anchor NODATA answer stays reusable.
- `reexpose` — a near delegation cut is removed, exposing the far-ancestor
  apex wildcard for the subtree.
- `side-branch` — the root changes via an unrelated subtree edit while
  untouched branches and the `kee`/`keep` string-prefix trap stay reusable.

Regenerate with:

```sh
go run ./cmd/gen-golden .
```

The output is byte-stable across runs.
