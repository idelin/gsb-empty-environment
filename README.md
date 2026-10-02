# compactidx

A read-only compact index over a real encoded byte image (Go standard
library only). Queries are served by binary search over the offset table
directly against the mapped bytes — no in-memory map.

## Byte layout (little-endian)

```
Header (48 bytes):
  0   magic "CIDX"   4   version u16   6   flags u16
  8   headerLen u32  12  entryCount u32
  16  tableOff u64   24  dataOff u64   32  dataLen u64   40  reserved u64
Offset table at tableOff: entryCount * u64 absolute record offsets
Record in [dataOff, dataOff+dataLen):
  uvarint keyLen | key | uvarint valLen | val | linkTag
  linkTag 0x00 = end of chain; 0x01 = followed by u64 continuation offset
  (continuation records have keyLen == 0; value = concatenation of chunks)
```

## Guarantees

- The whole offset graph is validated before any byte is trusted:
  out-of-file offsets, integer overflow, overlapping regions, cycles, and
  unsorted keys are all rejected. A locally valid record is never enough.
- Views/cursors borrow the mapping; close is deferred until the last
  borrow is released. Closing one view never invalidates the others.
- A reload installs a new generation; old handles keep reading their own
  generation and are never rebound. A failed load leaves the old mapping
  untouched. An empty file is not a valid empty index.
- Every runtime read is checked against the validated regions; the
  independent `layoutcheck` package re-derives the layout from raw bytes
  and verifies that observed reads never touched unvalidated bytes.

## Run

```
go test ./...        # tests, including controlled-interleaving lifecycle tests
go run ./cmd/cidxdemo  # layout + occupancy + lifecycle demo
```
