# stresc

Go library that embeds arbitrary Unicode text into a custom
double-quoted string format and decodes it back.

## Format

- Text is wrapped in `"..."`.
- `\"` and `\\` escape quote and backslash; `\a \b \f \n \r \t \v` are
  the C-style escapes; other control characters become `\xNN`.
- The decoder additionally accepts `\xNN` (ASCII only) and `\uXXXX`
  with UTF-16 surrogate pairs.
- Decoding rejects lone surrogates (escaped or UTF-8 encoded), invalid
  hex digits, raw control characters, and invalid UTF-8. All error
  offsets are byte offsets into the original input.

## One-shot API

- `Encode(dst, s) []byte` — append the quoted encoding of `s`.
- `Decode(src) (string, int, error)` — decode one quoted string,
  returning the text and bytes consumed.

## Streaming API

- `NewDecoder(sep)` / `Feed` / `Finish` — incremental decoding of
  separator-delimited records. Escape sequences and surrogate pairs may
  span chunks. After an error the decoder resynchronizes at the next
  separator that is not part of a legal escape sequence. `Processed()`
  is a checkpoint: the absolute offset up to which input is consumed.
- `NewEncoder(ctx, w)` / `Write` / `Close` — incremental encoding to
  an `io.Writer` with backpressure (short consume counts when the
  internal buffer cap `SetMaxBuffer` is reached) and context
  cancellation. `Consumed()` reports total input bytes consumed.

## Tests

`go test ./...` covers round trips over all short character
combinations, bad-escape mutations, split-point equivalence for every
chunk boundary, separator-inside-escape recovery, backpressure, and
cancellation.
