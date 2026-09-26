# blockstore

A persistent byte-sequence store in pure Go (standard library only).
Byte sequences live on fixed-size **physical blocks**; **slices** are
immutable logical views that share blocks, support cross-block reads, and
use **copy-on-write** for every edit. Memory accounting deduplicates by
physical block.

## Model

- `Store` owns all physical blocks, the memory ledger, and the live-slice
  registry. A block is reclaimed exactly when its last referencing slice
  is released — a live (unreleased) slice is a **borrow** that pins its
  blocks.
- `Slice` is an immutable logical byte string: an ordered chain of
  physical block ids plus a logical length. `Snapshot()` creates an
  independent branch in O(blocks) index time with **zero data copying**;
  edits to either branch never affect the other.
- `Editor` stages copy-on-write edits (overwrite / insert / delete /
  move / copy-within) inside one transaction. `Commit` publishes a new
  slice; `Cancel` — or a memory-limit overflow — **rolls the whole edit
  back**, temp blocks included.

## Index encoding layout (real bytes, not a map)

The authoritative block chain of a slice is a serialized byte string:

```
uvarint  format version (1)
uvarint  logical length in bytes
uvarint  block count N
varint   block id[0]              (zigzag)
varint   id[1]-id[0]              (zigzag delta)
...
varint   id[N-1]-id[N-2]          (zigzag delta)
```

Deltas are signed because reordering edits (Move) make ids non-monotonic.
Append-only chains cost ~1 byte per block instead of 8: a 1000-block slice
uses a ~1 KB index vs 8 KB for a naive fixed-width array. `Slice.RawIndex()`
exposes the bytes, `Slice.IndexBytes()` the occupied size.

## Semantics

- **Cross-block reads**: `ReadAt` (io.ReaderAt), `ReadRange`, and
  `Uint16/32/64(off, binary.ByteOrder)` read across block boundaries with
  exact endianness. Empty slices return `io.EOF` / `ErrOutOfRange`, never
  panic.
- **Copy-on-write splice**: whole untouched blocks are shared by
  reference; only boundary blocks mixing old and new bytes are physically
  copied. A 1-byte edit of a 1 MiB slice copies ≤ 1 block (64 B) and
  shares 16383/16384 blocks.
- **Move / CopyWithin**: the source range is snapshotted from the
  **pre-operation** content before any mutation, so overlapping
  source/destination ranges behave like memmove, never smear.
  `Move(dst, src, n)` deletes `[src, src+n)` and inserts those bytes at
  `dst` in the post-removal sequence.
- **Accounting**: `Stats` counts each physical block once regardless of
  how many slices reference it; temp blocks of in-flight edits are
  included (`TempBytes`, `PeakBytes`). `VerifyLedger()` independently
  walks all live slices, recounts every block reference, and
  cross-checks the ledger.

## Run

```
go test ./...
go run ./cmd/demo
```
