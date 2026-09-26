# cowbitmap

Fixed-length bitmap with block-level copy-on-write, in pure Go stdlib.

- **Block COW**: storage is `[]*block`, 512 bits (64 B) per block, nil = all-zero.
  `Snapshot()` copies only the pointer slice and bumps per-block refcounts;
  a write to a shared block clones just that block, so old snapshots never
  see new flips.
- **Lazy AND/OR**: `And`/`Or` retain operand blocks and compute result blocks
  on first `Get`, caching them. Length mismatch is rejected before any retain.
- **Refcount reclamation**: `Release` drops block refs; a second `Release`
  returns `ErrDoubleRelease` and refs never go negative. Clearing the last
  bit of a block reclaims it.
- **Real encoding** (not a wrapped map): 28 B header (magic, version, nbits,
  blockBits, nblocks, uniqBlocks) + deduplicated 64 B block data + 4 B/block
  index table. `EncodedLen()` predicts `len(Encode())` exactly.

```
go test ./...   # run tests
go run ./cmd/demo
```
