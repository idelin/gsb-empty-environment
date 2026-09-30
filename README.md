# stablecodec

A schema-driven binary serialization library in pure Go (standard library
only), built around stable field numbers and safe evolution.

## Wire format

```
message := uvarint(schemaVersion) field*
field   := uvarint(fieldNumber<<3 | wireType) payload
payload := varint | 8-byte LE | 4-byte LE | uvarint(length) bytes
```

## Supported serialization scope (deliberately closed)

- varint: `uint64` / `int64` / `bool` / enums
- fixed64 / fixed32: `float64` / `float32` bits, fixed-width integers
- length-delimited: `string`, `[]byte`, nested messages
- repeated fields of any of the above

Out of scope by design: maps, self-describing type tags, cyclic
references, embedded code or type URLs.

## Evolution rules (enforced, not just documented)

- Fields are identified by **stable numbers**; names are advisory.
- **Unknown fields are retained verbatim** and re-emitted on re-encode:
  an old program transcoding a new record loses nothing.
- **Adding optional fields is compatible** in both directions.
- **Deleted numbers are tombstoned** via `Schema.ReserveDeleted`;
  reusing one for an unrelated type is rejected.
- **Duplicate field numbers / type conflicts are rejected** at schema
  construction, and wire-type-vs-declared-type conflicts are rejected
  at decode time.
- **Missing required fields** fail decoding with a
  `MissingRequiredError` naming each field by number and name.
- **Version diagnostics**: `Decode` returns a `Diagnostics` value
  noting older/newer encoder versions and retained unknown-field count.

## Decoder safety

- Never executes host code: no reflection, no callbacks, no
  data-driven type dispatch. Decoding is pure data interpretation.
- **Every declared length is validated** against `MaxFieldSize` and the
  remaining input *before* any allocation.
- Hard caps: `MaxFieldNumber`, `MaxFieldSize` (1 MiB),
  `MaxFields` (65536), `MaxMessageSize` (64 MiB).
- Truncation and varint overflow are reported, never panics.

## Usage

```go
s := codec.NewSchema("person", 2)
s.AddField(codec.FieldDef{Number: 1, Name: "name", Type: codec.FBytes, Required: true})
s.AddField(codec.FieldDef{Number: 2, Name: "id",   Type: codec.FVarint, Required: true})
s.ReserveDeleted(3, "fax removed in v2")
s.AddField(codec.FieldDef{Number: 4, Name: "email", Type: codec.FBytes})

m := s.NewMessage()
m.SetString(1, "alice")
m.SetUint64(2, 42)
data, _ := m.Encode()

decoded, diag, err := s.Decode(data)
```

## Run

```sh
go test ./...   # test suite
go run ./cmd/demo
```
