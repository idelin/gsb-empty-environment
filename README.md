# cidx — compact read-only index with validated layout and leased mappings

Pure Go, standard library only. A compact sorted-string index with a real
binary encoding (not a wrapped `map`), an independent byte-layout checker,
and lease-based mapping lifetime management.

## Byte layout

All integers little-endian.

```
Header (40 bytes)
  [0:8]   magic "CIDXIDX1"
  [8:12]  format version uint32 (== 1)
  [12:16] entry count uint32
  [16:24] offset table absolute offset uint64
  [24:32] data region absolute offset uint64
  [32:40] data region length uint64
Offset table: entryCount+1 uint32 offsets, relative to data region start.
  Non-decreasing; offsets[0] == 0; offsets[last] == dataLength.
Data region: entryCount records. Record i spans
  [dataOffset+offsets[i], dataOffset+offsets[i+1]):
  uvarint keyLen | key | uvarint valueLen | value   (exact extent)
Keys strictly increasing (binary search). File ends exactly at
dataOffset+dataLength — no trailing bytes.
```

## Validation (independent checker)

`Check` (layout.go) is independent of the query path and rejects:

- empty files / short files (an empty file is **not** a valid empty index;
  a zero-entry index built by `Builder` still occupies bytes and is valid)
- bad magic, unsupported version
- integer overflow in any layout arithmetic (`entryCount+1`,
  `tableOffset+tableBytes`, `dataOffset+dataLength`)
- region overlap (header / offset table / data)
- offset-graph back-edges (cycles) and offsets outside the data region,
  including malicious offsets past end of file
- truncated files and trailing garbage
- malformed records (record must consume its extent exactly)
- global key-order violations — a record that parses locally is never
  trusted until every global invariant holds

`Check` records exactly which byte ranges it validated. The query path can
only read through `Guarded` (guarded.go), which refuses any read outside a
validated region.

## Lifetime model

- `Store` owns the current `Mapping`; `Load` validates first and swaps
  atomically. A failed load never disturbs the current mapping.
- Each `Mapping` has a generation. Views and cursors borrow a mapping;
  handles are bound to their generation and can never observe a new image.
- `Mapping.Close` only *requests* teardown: it completes when the last
  borrow is released. Existing borrowers keep working meanwhile; new
  borrows are refused with `ErrMappingClosing`.
- Closing one view never invalidates other views on the same mapping.
  Queries on a closed view fail with `ErrViewClosed`.

## Run

```
go test -race ./...
go run ./cmd/cidxdemo
```
