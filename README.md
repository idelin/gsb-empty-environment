# wiregw

A resumable parser for an explicit subset of a protobuf-style binary wire
format, implemented with only the Go standard library (no message codec
libraries). Built for a gateway that must not trust length fields.

## Wire subset

- Field numbers: 1 .. 2^29-1; key = `(field<<3)|wireType`
- Type 0: varint, at most 10 bytes, decoded as uint64 (overflow rejected)
- Type 1: 64-bit little-endian
- Type 2: varint length + raw bytes
- Type 5: 32-bit little-endian
- All other wire types are rejected

## Guarantees

- **Resumable**: input may arrive in arbitrary chunks (`Feed`); `Next`
  returns one field at a time and `ErrNeedMore` when paused mid-field.
- **Bounded**: total bytes, single-field length, fields per message and
  nesting depth are caller-set `Limits`; every limit is checked before
  the corresponding allocation or recursion.
- **Lossless**: fields appear in `Message.Fields` in wire order. The
  schema declares which length-delimited fields are nested messages or
  packed arrays; everything else keeps its raw bytes, so decode +
  re-encode never drops or reorders unknown fields.
- **Precise errors**: `*wire.Error` carries the absolute byte offset and
  the field-number path (e.g. `4.4`) of the failure. Overflowing varints,
  illegal field numbers, unsupported wire types, truncation and packed
  fixed-width payloads whose length is not a multiple of the element
  width are all rejected.

Note: packed varint arrays declared in the schema are re-encoded in
canonical varint form; unknown fields are always reproduced byte-for-byte.

## Layout

- `wire/` — the parser package (primitives, streaming parser, message
  model, schema, dump)
- `wire/fixtures_test.go` — hand-written byte fixtures: max uint64, empty
  length field, repeated fields, max field number, truncation at every
  byte, byte-by-byte and every-chunk-size feeding
- `wire/wire_test.go` — error offsets/paths, limits, round-trip
  preservation of unknown fields, stats
- `cmd/wiretool/` — CLI

## Test

```sh
go test ./...
```

## CLI

```sh
go run ./cmd/wiretool -i msg.bin -schema schema.json -chunk 4096 -dump -check -o out.bin \
  -max-total 1048576 -max-field-len 65536 -max-fields 10000 -max-depth 16
```

Schema JSON (wire types 0=varint, 1=fixed64, 5=fixed32):

```json
{"nested": {"4": {"packed": {"1": 5}}}, "packed": {"7": 0}}
```

The CLI feeds the input in `-chunk` byte pieces, prints a text dump,
verifies that re-encoding reproduces the input (`-check`), and reports
resource peaks on stderr: parser-level `peak_buffered` /
`bytes_allocated` plus process memory including a sampled `peak_alloc`.
