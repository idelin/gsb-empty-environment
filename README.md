# sparseidx

Append-only store of variable-length records with a sparse offset index.
Pure Go standard library. Records live in immutable blocks; updates never
rewrite old blocks.

## Design

- **Record** (`format.go`): `u32 crc32 | u8 flags | u16 keyLen | u32 valueLen | key | value`.
  Flags: put / tombstone / patch (partial value replacement: `u64 offset | bytes`).
- **Block** (`block.go`): `"SIB1" | u32 version | u64 blockID | records... | u32 count | u32 crc32`.
  Immutable once published; written via tmp+fsync+rename+dir-fsync.
- **Sparse index** (`index.go`): every `stride`-th record mapped to
  `(recordIndex, blockID, offset)`, with its own versioned, checksummed file.
  Lookups validate the record boundary at the indexed offset (checksum-verified
  decode) instead of trusting it; a non-boundary offset reports `ErrCorrupt`.
  Locating record k decodes at most `stride` records, crossing blocks as needed.
- **Manifest**: the committed block list, atomically renamed last, so a crash
  exposes either the old or the new state, never a mixture.
- **Recovery** (`store.go` `Open`): manifest is the source of truth; orphan
  blocks are ignored and collectable; a block failing strict validation keeps
  only its valid record prefix (a half-written record is never accepted); a
  missing/corrupt index is rebuilt from the blocks.
- **Snapshots & GC**: a snapshot pins its committed block list; `Compact`
  rewrites the live view into a fresh block; `CollectGarbage` only reclaims
  blocks that are neither committed nor pinned, so old snapshots never touch
  reclaimed blocks (audited in tests via an access hook).
- **Bounded decode**: `MaxRecordBytes` caps per-record payload; the length
  check runs before any allocation.

## Run

```sh
go test ./...     # tests
go run ./cmd/demo # end-to-end demo
```
