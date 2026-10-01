# taggedunion

Tagged-union binary codec in Go (standard library only).

## Wire format (big-endian)

```
batch: [count u32][item]...
item:  [tag u16][version u16][payloadLen u32][payload][crc32 IEEE over tag..payload]
```

## Supported serialization scope (deliberately closed)

- Field kinds: `uint8`, `int32`, `string` (u16 length prefix, max 65535 bytes).
- Payload ≤ `MaxPayload` (1 MiB), batch items ≤ `MaxItems` (65536).
- Decoding never executes host code; every length is validated against the
  remaining input **before** any allocation.

## Semantics

- Each (tag, version) branch has its own explicit field schema.
- Version evolution happens only through explicitly registered migration
  functions; the codec refuses to guess.
- Unknown tag, or known tag with unknown version: the item is kept opaque —
  version and checksum preserved, re-encoded byte-for-byte, never
  reinterpreted through a coincidentally matching older schema.
- A payload whose fields don't match its tag's schema (e.g. tag A carrying
  tag B fields) is rejected via strict bounds checks plus trailing-byte
  rejection.
- Empty branches (zero-length payload) are legal.
- Batch decode is atomic: any failure aborts the whole batch and reports
  the branch path (`batch[2].email`) and absolute byte offset.

## Run

```
go test ./...
go run ./cmd/demo
```
