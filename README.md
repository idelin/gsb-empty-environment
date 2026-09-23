# mtf — Move-To-Front transform with versioned alphabets

Pure-Go (standard library only) Move-To-Front transform over a configurable,
versioned alphabet, with an explicit unknown-symbol escape protocol,
resettable logarithmic-time state, and block-boundary checkpoints.

## Design

- **Alphabet** (`alphabet.go`): ordered set of distinct runes. Duplicates are
  illegal (`ErrDuplicateSymbol`). `Extend` produces a new alphabet at
  `Version+1`; alphabets are immutable.
- **Model** (`model.go`): MTF state backed by a Fenwick (binary indexed)
  tree over slots. Rank lookup, order-statistic selection and move-to-front
  are all `O(log n)`; symbols are prepended into fresh slots instead of
  shifting the whole alphabet, with amortized `O(n)` compaction. Slot
  capacity stays bounded (`2n+16`). `Reset` restores the initial order;
  `Checkpoint`/`Restore` snapshot and recover the full state at block
  boundaries — recovered processing matches continuous processing exactly.
- **Unknown-symbol policy** (`codec.go`): explicit, chosen by the caller.
  `PolicyReject` returns `ErrUnknownSymbol` without touching state.
  `PolicyEscapeUpdate` emits an in-band escape token (index == current
  alphabet length) followed by a literal token; both sides extend their
  alphabets identically — nothing is ever inserted privately by the encoder.
- **Index safety**: decode indices outside `[0, alphabetLen)` are rejected
  with `ErrIndexOutOfRange` and the model state is left unchanged.
- **Block format** (`codec.go`): magic `MTF1`, uvarint format version,
  alphabet version, token count, payload length, uvarint token payload,
  CRC32-IEEE of the payload. All are verified before/while decoding, and
  `DecodeBlock` enforces a caller-supplied output quota (`maxOutput`),
  rolling the model back on any failure.
- **Reference model** (`reference.go`): an independent naive list-based
  implementation used by the tests to cross-check every operation sequence
  and arbitrary breakpoint recovery against the Fenwick model.

## Layout

- `alphabet.go`, `model.go`, `codec.go`, `reference.go` — package `mtf`
- `mtf_test.go` — tests (`go test`)
- `cmd/mtfdemo` — runnable demo

## Run

```sh
go test ./... -v     # tests: reference cross-check, breakpoints, tampering, quota
go run ./cmd/mtfdemo # end-to-end demo
```
