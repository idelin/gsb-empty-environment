# mtf — Move-To-Front transform with versioned alphabets

Pure-Go (stdlib only) MTF encoder/decoder over a configurable alphabet.
No compression libraries are used.

## Design

- **Log-time core**: the MTF list is an implicit treap with subtree sizes.
  Symbol→index, index→symbol and move-to-front are all O(log n) expected;
  the alphabet is never linearly re-shuffled (`treap.go`).
- **Configurable, versioned alphabet** (`alphabet.go`): duplicate symbols
  and empty alphabets are rejected at construction. The base alphabet is
  version 1; each extension bumps the version.
- **Explicit unknown-symbol policy** (`stream.go`): `RejectUnknown` fails
  the encode; `EscapeUnknown` emits an escape token — index equal to the
  current alphabet size, followed by the literal byte — and both sides then
  extend the alphabet and move the new symbol to the front. The encoder
  never privately mutates shared state.
- **Invalid indices are rejected** (`DecodeIndex`) and leave the model
  state untouched.
- **Reset**: `Model.Reset` restores the canonical alphabet order.

## Checkpoint format (block boundaries)

Little-endian, `checkpoint.go`:

| offset | field                | size  |
|--------|----------------------|-------|
| 0      | magic `"MTFC"`       | 4     |
| 4      | format version (=1)  | 2     |
| 6      | alphabet version     | 4     |
| 10     | state length         | 4     |
| 14     | MTF order payload    | stateLen |
| 14+stateLen | CRC-32/IEEE     | 4     |

Restore verifies magic, format version, total length, CRC, alphabet
version, and that the payload is a permutation of the alphabet. A model
restored from a checkpoint produces results identical to uninterrupted
processing (tested).

## Stream format

A block is a sequence of uvarint tokens. Token `< alphabet size` is an MTF
index; token `== alphabet size` is the escape token and is followed by one
literal byte (the new symbol). Decoding is bounded by a caller-supplied
output limit (`ErrOutputLimit`).

## Testing

`property_test.go` cross-checks the treap model against an independent
naive slice-based reference over randomized operation sequences (encode,
decode incl. out-of-range, reset, checkpoint/restore at arbitrary
breakpoints), comparing full state after every operation.

```sh
go test ./...   # run all tests
go run ./cmd/mtfdemo   # run the demo
```
