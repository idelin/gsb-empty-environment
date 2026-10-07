# schemamig

Schema migration for row batches over four value kinds: `int`, `rational`,
`text`, and `null`.

## Conversion rule

A conversion between kinds is allowed **only if a round trip back to the
source kind reproduces the original value exactly** (`Convert` in
`value.go`). Widening (`int → rational → text`) always qualifies;
narrowing is checked per value:

- `3/1 → int` ok, `7/2 → int` rejected
- `"42" → int` ok, `"007" → int` rejected (would come back `"7"`)
- `"3/4" → rational` ok, `"1.5" → rational` rejected (would come back `"3/2"`)

Null never converts; nullability is enforced at the field level.

## Row migration (`Schema.MigrateRow`)

- Missing field → explicit `Field.Default` if declared, else null if
  `Nullable`, else a `FieldError`.
- Unknown input fields are preserved verbatim in the single `_extra`
  record column (`ExtraColumn`).

## Batch migration (`Schema.MigrateBatch`)

Publishing is atomic: if any row fails, no rows are returned and the
`BatchError` carries one `RowError` per failure with a dotted path such
as `rows[2].price`.

## Tests

`schemamig_test.go` includes a round-trip property test across all
value/kind pairs plus targeted tests for type narrowing, nulls, defaults,
unknown-field preservation, and batch atomicity.

```sh
go test ./...
```
