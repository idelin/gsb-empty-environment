# framemux

`framemux` multiplexes many logical streams over a single ordered byte
channel, using a flag-delimited, escape-protected frame protocol. Pure
Go standard library.

## Frame format

On the wire each frame is `FLAG escaped(content) FLAG`
(`FLAG = 0x7E`). Content layout:

| field    | size | notes                                  |
|----------|------|----------------------------------------|
| epoch    | 1    | encoding epoch (escape profile)        |
| stream   | 1    | logical stream ID (0-255)              |
| seq      | 4    | per-stream sequence number (BE)        |
| type     | 1    | DATA / ACK / EPOCH_PROPOSE / EPOCH_ACK / RESET |
| length   | 2    | payload length (BE)                    |
| payload  | n    |                                        |
| crc32    | 4    | IEEE, over everything above            |

## Escaping and epochs

`0x7D` introduces an escaped byte (`byte ^ 0x20`). Decoding is uniform;
the *encoding epoch* only defines which bytes the sender must escape
(epoch 1 additionally escapes `0x00 0x11 0x13 0xFF`). Epochs change
exclusively at frame boundaries after mutual confirmation
(`ProposeEpoch` -> EPOCH_PROPOSE -> EPOCH_ACK). In-flight tail frames
from the previous epoch are tolerated during a bounded grace window.

## Robustness properties

- **Arbitrary chunking** — all parser state (including a split escape
  pair) lives in the `Mux`; `Feed` accepts any chunk sizes and pausing
  preserves incomplete escapes and negotiation state.
- **Bounded resync** — a lost delimiter or dangling escape abandons the
  current frame; buffered content never exceeds `maxContent` and every
  input byte is scanned exactly once. Payload bytes that look like the
  magic flag are always escaped, so pseudo-magic never starts a frame.
- **Need-more vs bad data** — incomplete input simply stays buffered;
  corruption surfaces as non-fatal `EventBadData` events and the
  connection resynchronizes at the next boundary.
- **Backpressure** — per-stream and global queued-byte quotas are
  independent; quota checks happen before a frame is admitted/allocated,
  and `Feed` returns `ErrBackpressure` until the application drains with
  `Receive` and calls `Resume`.
- **Isolation & recovery** — a corrupt frame or a stream `RESET` never
  disturbs other streams. Receivers ACK every DATA frame and deduplicate
  by sequence number; `Retransmit` replays unacknowledged frames after
  loss, so recovered streams equal the originals with no duplicates.

## Usage

```go
tx := framemux.NewMux(w, framemux.Config{}) // w: io.Writer to the peer
tx.Send(1, []byte("hello"))
tx.ProposeEpoch(1)
_ = tx.Feed(chunk)          // returns framemux.ErrBackpressure when quotas block
p, ok := tx.Receive(1)      // drain queued payloads
_ = tx.Resume()             // admit frames held by backpressure
_ = tx.Retransmit()         // replay unacked frames after loss
```

## Run

```sh
go test ./...   # protocol test suite
go run ./example
```
