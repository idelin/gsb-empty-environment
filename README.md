# integrity

Go library building a binary Merkle digest tree over a fixed number of
blocks, with membership, batch and update proofs.

## Design

- **Fixed-numbered blocks**: block `i` of `N` is a leaf; the leaf hash
  binds the block digest, its index and the total count
  (`H(0x00 || index || total || blockHash)`), so a leaf cannot be
  replayed at another position or under another snapshot size.
- **Explicit empty leaves**: a missing block (nil hash) and padding to
  the next power of two use `H(0x02 || index || total)`, which cannot
  collide with any real block — including a block of all-zero bytes.
- **Interior nodes**: `H(0x01 || left || right)`.
- **Local updates**: `Update` recomputes only the path from the changed
  leaf to the root and returns an `UpdateProof` binding the old root,
  the new root and the block count.
- **Snapshot consistency**: all proof-generation methods take a read
  lock and return the root they were generated against, so concurrent
  updates can never splice a proof onto a root that never existed.

## Verification

- `VerifyProof(root, proof)` — single block membership.
- `VerifyBatchProof(root, batchProof)` — many blocks of one snapshot;
  shared path nodes are recomputed and any conflicting provided node is
  rejected.
- `VerifyUpdateProof(updateProof)` — both paths must recompute to the
  bound roots and every shared sibling must be identical.

Run the tests (including full-rebuild cross-checks, empty-file handling,
tampered nodes, malicious mixed proofs and concurrent updates):

```sh
go test -race ./...
```
