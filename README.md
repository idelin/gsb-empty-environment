# framemux

Frame protocol multiplexing several logical streams over one byte channel
(Go, standard library only).

## Frame format

```
MAGIC0 MAGIC1 | Type Stream Seq Epoch LenLo LenHi | Payload... | CRC32-LE
```

- `MAGIC = 7E 5A` delimits frames; it never appears unescaped inside a frame.
- Header carries stream ID, sequence number, encoding epoch and length.
- CRC32 (IEEE) covers header + payload.

## Properties

- **Escape epochs.** Byte-stuffing rules are versioned by epoch
  (`ProposeEpoch` → peer confirms → both switch at a frame boundary).
  Old-epoch tail frames interleaved with the confirmation are still decoded
  during a bounded grace window.
- **Bounded resync.** Lost delimiters, dangling escapes and corrupt frames
  trigger a rescan bounded by the max frame size; delimiter scans are
  budgeted (`MaxResyncScan`) and surface `ErrNoDelimiter`. Payload bytes
  equal to the magic are escaped, so pseudo-magic never starts a frame.
- **Backpressure.** Per-stream quotas are independent of the global buffer
  quota; `Pause`/`Resume` hold and release frames while preserving
  incomplete escape state and negotiation state. Length quotas are checked
  before any payload allocation.
- **Chunked input.** `Write` accepts arbitrary splits (including between an
  escape byte and its operand) and distinguishes `ErrNeedMoreData` from
  `ErrBadData`.
- **No duplicate delivery.** Retransmitted, already-acknowledged frames are
  dropped by sequence number.

## Run

```sh
go test ./...      # tests
go run ./examples/demo
```
