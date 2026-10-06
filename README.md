# binparse

Go library for a length-prefixed binary record format. The byte order is
chosen by a flag in the file header; records carry an optional-field
bitmap and may contain length-prefixed nested bodies.

## Format

All multi-byte integers use the header-selected byte order.

```
header: magic "PGB1" | order byte (1=LE, 2=BE) | version byte | record count uint32
record: body length uint32 | body
body:   optional-field bitmap uint32 | field*
field:  tag uint8 | value length uint16 | value
```

Known tags: `0x01` ID (uint32, fixed 4), `0x02` Name (variable),
`0x03` Flags (uint16, fixed 2), `0x04` Nested (nested body).

## Rules enforced by the parser

- Unknown tags are skipped by length; their payload is opaque.
- A known field with a mismatched fixed length is an error
  (`ErrLengthMismatch`).
- No field or nested body may extend past its enclosing body
  (`ErrOutOfBounds`); a record may not overrun the file.
- Bitmap consistency: known fields must be declared, declared fields
  must be present, no duplicates, no reserved bits (`ErrBitmap`).
- Nesting depth is capped; trailing bytes after the last record are
  rejected. `Parse` never panics on malformed input.

## Usage

```go
enc := binparse.NewEncoder(binparse.LittleEndian)
enc.Record().ID(42).Name("hello").Nested(func(n *binparse.RecordEncoder) {
    n.Flags(3)
})
data := enc.Bytes()

f, err := binparse.Parse(data)
```

## Tests

`go test ./...` runs round-trip checks for both byte orders, verifies
reported offsets against a hand-computed layout, truncates a generated
file byte-by-byte requiring an error at every prefix, and injects
corruptions (length mismatch, body overrun, bitmap violations) at
offsets discovered through the parser itself.
