# sframe

Structured binary frames with streaming parse, field redaction, and
recomputed length/checksum. Standard library only.

## Frame format (big-endian)

| Offset | Size | Field                              |
|--------|------|------------------------------------|
| 0      | 2    | magic `SF`                         |
| 2      | 1    | version (1)                        |
| 3      | 1    | flags                              |
| 4      | 8    | sequence (preserved for replay)    |
| 12     | 8    | timestamp unix-nano (preserved)    |
| 20     | 4    | payload length                     |
| 24     | n    | TLV payload (nestable objects)     |
| 24+n   | 4    | CRC32-IEEE over header + payload   |

TLV field: `nameLen(1) name type(1) valueLen(4) value`. Types: string,
int64, bool, object (nested fields), bytes. Unknown fields round-trip
verbatim, so extensions survive redaction.

## Guarantees

- `Parser.Feed` accepts arbitrary chunking; truncation yields zero frames
  with nil error, corruption yields `*CorruptError` (`NeedMore` distinguishes
  the two at the `DecodeFrame` level).
- Payload length quota is checked before any allocation or wait.
- `Redactor.Redact` replaces values at dot-separated paths, recomputes all
  length prefixes and the CRC, and never touches seq/timestamp.
- Strict mode rejects the frame (nil output) when a path is missing.
- A leak guard rejects output if redacted bytes survive anywhere in the
  payload (e.g. duplicated inside an unknown extension field).
- Errors carry only paths/offsets/lengths, never field values.

## Run

```sh
go test ./...
go run ./cmd/demo
```
