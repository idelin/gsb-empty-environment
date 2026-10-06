# qstr

`qstr` embeds arbitrary Unicode text into custom double-quoted strings and
decodes them back, in one shot or as a stream.

## Wire format

A record is a double-quoted string. Inside the quotes, backslash, double
quote and control characters (U+0000–U+001F, U+007F) must be escaped;
everything else appears as itself in UTF-8. Only three escape forms exist:

| escape   | meaning                          |
|----------|----------------------------------|
| `\\`     | backslash                        |
| `\"`     | double quote                     |
| `\uXXXX` | any code point (hex, any case)   |

A high surrogate escape (`\uD800`–`\uDBFF`) is legal only when immediately
followed by a low surrogate escape (`\uDC00`–`\uDFFF`); the pair decodes to
the combined code point. Lone surrogates, unknown escapes, invalid hex
digits, raw control characters and invalid UTF-8 are rejected. All error
offsets are byte offsets into the original input. Decoding is single-pass:
escapes resolve exactly once and decoded output is never re-interpreted.

## One-shot API

```go
enc := qstr.Encode("any \"text\"\n")   // -> quoted wire form
dec, consumed, err := qstr.Decode(enc) // consumed = bytes through closing quote
```

`Decode` parses one record from the head of the input; bytes after the
closing quote are left to the caller via the consumed count. Failures
return `*qstr.Error` with `Start`/`End` byte offsets.

## Streaming

`StreamEncoder` encodes incrementally into a bounded buffer; `Write`
blocks under backpressure until the reader drains it or the encoder's
context is cancelled. Multi-byte UTF-8 characters may span `Write` calls.

`StreamDecoder` decodes a stream of records separated by a caller-chosen
byte. Escape sequences and surrogate pairs may span `Write` boundaries;
`Checkpoint` reports how many stream bytes are fully consumed. On error it
records a `StreamError` (absolute offsets) and resynchronizes at the next
separator that does not occur inside a legal escape sequence.

## Tests

```
go test ./...
```

Coverage includes: round trips over all 0–3 character combinations of a
tricky alphabet, bad-escape mutations (corrupted hex, truncations, lone
surrogates), single-pass decoding guarantees, exact error offsets, stream
decoding/encoding at every byte cut point, separator-inside-escape
recovery, and encoder backpressure/cancellation.
