# rowconv — declarative row converter (Go standard library only)

Converts rows from a source schema to a target schema without silent
precision loss. No external database is involved; the CLI is pure
JSON-in / JSON-out.

## Types

| Type        | Wire form (input row)                              | Canonical output |
|-------------|----------------------------------------------------|------------------|
| `int`       | JSON integer, `bits`: 8/16/32/64 (default 64)      | JSON integer     |
| `decimal`   | `{"coefficient": "<int>", "scale": n}`             | same object      |
| `string`    | JSON string (must be valid UTF-8)                  | JSON string      |
| `bytes`     | base64 string                                      | base64 string    |
| `timestamp` | RFC3339 with explicit offset, e.g. `2024-02-29T23:30:00.123456+05:30` | integer UTC microseconds |

## Semantics

- **Precision loss is never silent.** Decimal downscaling and
  sub-microsecond timestamps are rejected by default
  (`"rounding": "reject"`); `"ties-to-even"` opts into round-half-to-even.
- **Null vs missing.** A JSON `null` is an explicit null; an absent key is
  missing. Per target field, in order: `default` wins, then `required`
  missing is an error, explicit null on a non-nullable field is an error,
  nullable fields become null, otherwise the field is omitted.
- **Checked limits.** Integer range (`bits`), decimal `precision`,
  string/bytes `maxBytes`, and UTF-8 validity all have explicit errors.
- **Atomic rows.** A failed row produces no partial output.
- **Ordered batches.** Result `i` always corresponds to input row `i`.

## CLI

```sh
go build -o rowconv ./cmd/rowconv
./rowconv examples/reject.json   # or: cat examples/reject.json | ./rowconv
```

Request: `sourceSchema`, `targetSchema`, `mapping` (source name -> target
name), `options.rounding`, `rows`. Response: `results[]` with `index`,
`ok`, and either `row` or `error`. See `examples/` for real input/output
pairs (`reject.json`/`reject.out.json`, `round.json`/`round.out.json`).

## Library

```go
conv, err := rowconv.NewConverter(src, tgt, mapping, rowconv.Options{Rounding: rowconv.Reject})
results := conv.ConvertBatch(rows) // []rowconv.Result, order preserved
```

## Tests

```sh
go test ./...
```

Covers negative half-to-even rounding, integer/decimal overflow, leap
days, offset day-crossing, invalid UTF-8, null/default/missing paths,
sub-microsecond timestamps, row atomicity, batch ordering, and an
independent `math/big.Rat` cross-check of all decimal rescaling.
