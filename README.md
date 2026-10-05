# varint — schema-driven varint stream codec

Signed/unsigned variable-length integer stream encoding and decoding,
with field bit widths supplied by a versioned schema.

## Encoding

- Unsigned fields: LEB128 (7 bits per byte, MSB = continuation).
- Signed fields: zigzag + LEB128 (`-1 -> 0x01`, `1 -> 0x02`, ...).
- Every value has exactly one canonical (shortest) byte string.

## Guarantees

- **Width checking** — values that do not fit the schema field's bit
  width are rejected with `ErrOverflow`, both when encoding and when
  decoding (excess payload bits or a continuation past the last
  permissible byte).
- **Canonical form** — non-shortest encodings (e.g. a trailing zero
  group) are rejected with `ErrNonCanonical`; re-encoding a decoded
  record reproduces the unique canonical bytes.
- **Truncation** — a stream ending mid-varint yields `ErrNeedMore`,
  never a zero value; nothing is consumed until a record completes.
- **Pause/resume** — `Decoder.Snapshot` captures undecoded bytes plus
  the schema version; `Resume` rejects a version mismatch with
  `ErrSchemaVersion`.
- **Batch atomicity** — `DecodeBatch` commits records one at a time;
  a record that fails decoding is not committed (its bytes stay
  buffered) and the error is returned with the records decoded so far.

## Layout

- `varint/varint.go` — low-level canonical varint encode/decode
- `varint/schema.go` — `Schema`, `Field`, `Value`, `Record`
- `varint/encoder.go` — `AppendRecord`
- `varint/decoder.go` — streaming `Decoder`, `State`, `Resume`
- `varint/varint_test.go` — boundary integers, byte-by-byte
  truncation, canonical re-encode, batch rollback, pause/resume

## Test

```sh
GO111MODULE=on go test ./...
```
