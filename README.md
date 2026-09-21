# deltastream

Per-channel delta codec in Go (standard library only).

## Wire format

Every frame is length-prefixed, so the byte stream may be chunked
arbitrarily and bad frames are detectable:

    [len:1][type:1][chan:1][payload:len-2]

| type | name    | payload                    |
|------|---------|----------------------------|
| 0x01 | anchor  | signed varint full value   |
| 0x02 | delta   | signed varint difference   |
| 0x03 | reset   | empty; clears baseline     |
| 0x04 | missing | empty; absent sample       |

## Semantics

- Each channel keeps its own baseline; channels are fully isolated.
- A delta with no baseline (never anchored, after reset, or after a lost
  frame) yields `EventUndecodable` — the decoder never guesses.
- A corrupt frame poisons only its channel until the next anchor.
- `missing` marks an absent sample but keeps the delta chain alive.
- The length prefix is validated against `MaxFrameLen` before any
  allocation; a bad length is a fatal stream error (resync impossible).
- Negative deltas use zigzag varints; int64 overflow is detected on both
  encode (falls back to anchor) and decode (poisons the channel).
- The encoder emits periodic full anchors every N samples to bound the
  poison window.

## Run

    go test ./...
    go run ./cmd/demo
