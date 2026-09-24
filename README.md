# wiremsg

Schema-driven binary TLV codec (Go, standard library only) that preserves
unknown fields byte-for-byte.

## Wire format (self-defined scope)

```
field := tag value
tag   := varint(field_number << 3 | wire_type)
```

| wire type | meaning                        |
|-----------|--------------------------------|
| 0         | varint (up to 64 bit)          |
| 1         | fixed64 (8 bytes, little-endian)|
| 2         | length-delimited (varint len + bytes) |
| 5         | fixed32 (4 bytes, little-endian)|

Schema kinds: `Varint`, `Fixed64`, `Fixed32`, `Bytes`, `String`,
`Message` (nested, length-delimited). Anything else (groups, packed
encodings, maps) is intentionally out of scope.

## Guarantees

- **Known fields** decode to typed `Value`s; **unknown fields** keep their
  complete raw encoding (tag + payload) as a zero-copy shared subslice of
  the input, in a list — never a map — so nested and repeated unknown
  fields survive with identity and relative order.
- **Mutation is immutable**: `Set`/`Append`/`Delete`/`Upgrade`/`Downgrade`
  return a new `Message`. On any error the original is untouched
  (failure atomicity).
- **Order policy**: decode preserves wire order; `Set` replaces in place;
  new fields append at the end; unknown segments never move. Re-encoding
  recomputes all offsets (`Offsets()`), so growing a known field shifts
  later unknown offsets correctly.
- **Conflicts are loud**: a singular known field occurring twice (on decode
  or on schema upgrade) returns `*ConflictError`; it is never silently
  resolved as last-wins.
- **Schema evolution**: `Upgrade`/`Downgrade` (both `Reschema`) re-parse
  raw segments that become known, and re-encode known values to raw
  segments that become unknown — in place, atomically.
- **Safety**: decoding executes no host code; every declared length is
  validated against the remaining input before any slice or allocation;
  `Budget{MaxFields, MaxDepth, MaxValueBytes}` bounds work and `Usage`
  reports actual consumption.
- **Memory**: raw segments share the input buffer; call `Compact()` to
  detach before releasing a large input.

## Run

```sh
go test ./...      # byte-level reference tests
go run ./cmd/demo  # end-to-end sample
```
