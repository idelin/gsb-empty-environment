# infer

Incremental column-type inference for streams of string records (e.g.
CSV/TSV imports). Each column is assigned the narrowest type that can
losslessly represent every non-empty value seen so far:

    bool < int < rational < text

## Semantics

- **bool** — exactly `true` / `false` (lowercase).
- **int** — canonical base-10 integers fitting `int64`. Leading zeros
  (`007`, `01234`) and signed zero (`-0`) are *text*: their spelling
  would not survive a numeric round trip (zip codes, IDs).
- **rational** — decimal/scientific literals representable as a finite
  `float64` (`3.14`, `0.1`, `1e5`). Integer literals beyond `int64`
  qualify only when exactly representable (`2^63`, `1e20` are;
  `1e20 - 1` is not, so it stays text). `NaN`, `Inf`, hex floats and
  `1e400` are text.
- **text** — everything else. By default the empty string is text
  evidence, not a null; `WithEmptyAsNull()` makes empty strings
  nulls that contribute no type evidence.

A column with no evidence (empty, or all nulls) falls back to `text`,
the safe default.

## Usage

```go
inf := infer.New([]string{"id", "score"})
_ = inf.AddRecord([]string{"007", "3.5"})
_ = inf.AddRecord([]string{"042", "2.75"})
fmt.Println(inf.Types()) // [text rational]

// Undo: removing records can narrow a column's type again.
_ = inf.RemoveRecord([]string{"007", "3.5"})
fmt.Println(inf.Types()) // [text rational]  (id stays text: "042")
```

Every column keeps per-class evidence counts (`Column(i).Counts()`),
which is what makes undo possible: removing a record decrements exactly
the counters its values incremented.

## Full-scan verification

`Verify(records)` re-infers all columns from scratch and reports any
divergence from the incremental state — useful to audit edge cases
such as leading-zero text, huge integers, and all-empty columns:

```go
mismatches, err := inf.Verify(records)
```

## Testing

```sh
go test ./...
```
