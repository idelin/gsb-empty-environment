# configcomplete

Context-aware completion for a configuration language with nested objects,
arrays and escaped strings (JSON grammar), in Go.

## API

```go
res := configcomplete.Complete(text, cursor)
```

- `cursor` is a **byte offset** into the original text; only `text[:cursor]`
  is analysed. Inside strings the cursor counts raw source bytes, so it may
  sit between the bytes of an escape sequence (`\` | `n`) or of a multi-byte
  UTF-8 rune.
- `res.OK == true`: the prefix can be extended into a valid document.
  - `res.Categories` — the token categories legal at the cursor
    (`object-start`, `object-end`, `array-start`, `array-end`, `colon`,
    `comma`, `string`, `number`, `boolean`, `null`). When the cursor is
    inside or right after an extendable token (unterminated string, partial
    keyword, number), that token's category is included. Empty when the
    document is already complete.
  - `res.Suffix` — the **shortest** string that, appended at the cursor,
    makes the whole text a valid document (`""` if it already is one).
- `res.OK == false`: the prefix is already invalid; `res.ErrOffset` is the
  **earliest** byte offset at which the text stops being extendable into a
  valid document (lexical or grammatical conflict).

The completion only ever appends at the cursor: every valid token before
the cursor is preserved unchanged (an incomplete token *at* the cursor may
be continued, e.g. `[1` → `[1]` or `[12]`).

## Examples

| input        | categories                          | suffix   |
|--------------|-------------------------------------|----------|
| ``           | object/array start, string, number, boolean, null | `1` |
| `{`          | object-end, string                  | `}`      |
| `{"a`        | string                              | `":1}`   |
| `{"a":`      | (any value)                         | `1}`     |
| `[1`         | array-end, comma, number            | `]`      |
| `["a\`       | string                              | `n"]`    |
| `[tr`        | boolean                             | `ue]`    |
| `{"a":1,`    | string                              | `"":1}`  |
| `{"a":1 "b"` | — error at offset 7                 |          |

## Verification

`truncate_test.go` cross-checks the implementation by generating random
complete documents and truncating them at every byte offset. For each
truncation it asserts, using the sample itself plus `encoding/json` as an
independent oracle (no hard-coded context tables):

- the category of the token that actually follows in the complete sample is
  contained in the returned categories;
- `prefix + suffix` is valid JSON;
- the suffix is minimal (last-byte removal stays invalid; short suffixes
  are brute-force proven shortest over a representative alphabet);
- every complete token of the prefix survives unchanged in the completed
  document.

Run: `go test ./...` and `go test -fuzz=FuzzComplete -fuzztime=30s .`
