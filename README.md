# wiremsg

A schema-driven TLV wire format in pure Go (standard library only) that
preserves unknown fields **byte-for-byte** across parse → mutate → marshal.

## Supported serialization scope (complete)

```
field  := tag payload
tag    := varint(fieldNumber<<3 | wireType)   // fieldNumber >= 1
wire 0 varint  -> uint64            payload: varint
wire 5 fixed32 -> uint32            payload: 4 bytes little-endian
wire 1 fixed64 -> uint64            payload: 8 bytes little-endian
wire 2 bytes   -> []byte | string | nested message
                                    payload: varint(length) + length bytes
```

Groups, zigzag varints, floats, maps and extensions are out of scope and
rejected on decode. **Decoding never executes host code**: it is table-driven
byte parsing only, and every length prefix is validated against the remaining
input *before* any slice is taken or allocation made.

## Guarantees

- **Unknown fields preserved**: kept as raw sub-slices (tag + payload) that
  share the input buffer — zero-copy. Bytes, relative order and `*Field`
  identity survive mutation of known fields.
- **No map semantics**: the message is an ordered field list. Repeated and
  unknown fields are never overwritten or reordered.
- **Explicit order policy**: parse records wire order; marshal emits list
  order; `Set` replaces the first match in place or appends; `Append` always
  appends at the end. Re-marshal recomputes all offsets, so growing a known
  field shifts unknown fragments without corrupting them.
- **Conflicts never silently resolve**: a duplicate non-repeated known field
  or a wire-type mismatch after a schema upgrade is `ErrConflict`, never
  last-wins.
- **Atomic failure**: `Parse` returns a nil message on any error; `Marshal`
  returns nil on any error; `Set`/`Append` validate before mutating.
- **Budget accounting**: `Budget` caps total bytes, field count and nesting
  depth; consumption is observable via `Used()`.

## Usage

```go
schema := wiremsg.NewSchema(map[uint32]wiremsg.FieldDef{
    1: {Name: "count", Type: wiremsg.TypeUint64},
    2: {Name: "name",  Type: wiremsg.TypeString},
})
m, err := wiremsg.Parse(schema, wireBytes, wiremsg.NewBudget(wiremsg.DefaultLimits))
m.Set(1, wiremsg.Value{Uint: 1 << 28})   // unknown fields keep their bytes
out, err := m.Marshal()
```

## Run

```
go test ./...     # byte-level reference tests (upgrade/downgrade/reorder/atomicity)
go run ./cmd/demo # end-to-end sample with hexdump output
```
