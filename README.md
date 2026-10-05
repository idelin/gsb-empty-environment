# rowconv — declarative row converter (Go standard library only)

Converts rows from a source schema to a target schema with explicit,
fail-fast semantics for every narrowing step. No external database, no
third-party dependencies.

## Types

- `int` — signed 64-bit integer, optional `min`/`max` on the target field
- `decimal` — fixed-point, `{"$decimal": {"coef": "<int64>", "scale": n}}`,
  value = coef * 10^-scale
- `string` — must be valid UTF-8, optional `max_bytes`
- `bytes` — base64 in JSON (`{"$bytes": "..."}`), optional `max_bytes`
- `timestamp` — input is RFC 3339 text with explicit offset
  (`{"$ts": "2024-02-29T23:30:00+05:30"}`), output is UTC integer
  microseconds (`{"$ts_micros": 1709233200000000}`)

`null` (JSON `null`) is distinct from a missing key. Missing keys use the
field `default` if present, else become `null` unless the field is
`required`. Explicit `null` on a `required` field is an error.

## Narrowing rules

- Decimal scale reduction: values not divisible by 10^diff are rejected
  (`"decimal_mode": "reject"`, the default) or rounded ties-to-even
  (`"half_even"`). Scale increases that overflow int64 are `overflow` errors.
- Sub-microsecond timestamp digits: rejected (`"timestamp_mode": "reject"`,
  default) or rounded ties-to-even (`"half_even"`).
- Row conversion is atomic: a failing row yields no partial output.
- Batch conversion preserves order: `results[i]` corresponds to `rows[i]`.

## CLI

```sh
go run ./cmd/rowconv < examples/input.json
```

Input: `source_schema`, `target_schema`, `mapping` (target -> source name;
unmapped fields use their own name), `config`, `rows`. Output: one result
per row with `index`, `ok`, and either `row` or a structured `error`
(`field`, `code`, `message`).

## Tests

```sh
go test ./...
```

Covers negative half-value ties-to-even rounding, int64 overflow, leap
days, offsets crossing midnight, invalid UTF-8, null vs. default handling,
atomicity, and batch ordering. `rational_test.go` cross-checks every
numeric conversion against an independent `math/big.Rat` reference over
20k deterministic pseudo-random cases.
