# dod — delta-of-delta time series archive (Go, stdlib only)

Append-only compressed archive for `(time, value)` integer series. No
third-party compression libraries; only the Go standard library.

## Format

Archive = 4-byte magic `DOD1` + 1-byte format version, then blocks.

Block:

```
uvarint totalLen            (payload + 4 crc bytes)
payload:
  byte    format version
  byte    flags (0)
  uvarint epoch             explicit reset epoch
  uvarint count
  svarint t0, v0            anchor
  [n>=2] svarint dt0, dv0   first deltas
  [n>=3] uvarint len + time dod stream
         uvarint len + value dod stream
uint32  crc32-IEEE(payload)
```

A dod stream is a list of segments `(width byte, count uvarint)` followed by
packed zigzag-encoded delta-of-delta values at `width` bits each
(`width` in `{0,2,4,8,16,32,64}`).

The index (separately serializable via `Index.Encode`) maps
`(epoch, t0, t1) -> (offset, length)` per block with a version counter;
superseded entries are retained for old-version inspection.

## Properties

- **Joint optimization**: a DP over anchor positions uses the exact encoded
  block size (with per-segment width DP inside) plus index overhead as cost.
- **Resets**: time regression (policy `AllowRegression`) or `Reset()` starts
  a new block with an explicit, incremented epoch. `RejectRegression`
  returns `ErrRegression` instead.
- **No overflow**: all delta arithmetic is checked; positions that would
  overflow int64 force a fresh anchor block.
- **Local decode / seek**: blocks are self-contained; `SeekTime` +
  `ReadBlock` decode one block, `ReadRange` decodes only overlapping blocks.
- **Local correction**: `Correct` re-encodes one block, appends the new
  bytes, and versions the index (old entry kept, marked superseded).
- **Append-only**: `Flush`/`Correct` never rewrite existing bytes.
- **Bounded decode**: `SetMaxValues` and `ReadRange`'s `maxOut` cap output.

## Run

```
go test ./...
go run ./cmd/doddemo
```
