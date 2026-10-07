# infer

Go library that infers, per column, the narrowest type — `bool`,
`int`, `rational`, or `text` — that can losslessly represent every
non-empty value seen so far.

## Rules

- **bool**: exactly `true` or `false`.
- **int**: canonical base-10 integer, optional `-`, no leading zeros
  (`0` allowed, `-0` and `007` are text). No size limit: values beyond
  `int64`/`uint64` range are still `int`.
- **rational**: canonical decimal (`3.14`, `-0.5`) or fraction
  (`22/7`) with no leading zeros and a non-zero denominator. Any
  finite decimal or fraction is exactly representable as a rational.
- **text**: everything else. The empty string is a text value, **not**
  a null.

An empty column (no values) defaults to `bool`, the narrowest type.

## Incremental updates

`Column` keeps one evidence count per kind. `Add` records a value;
`Remove` retracts it, so the inferred type can narrow again once the
widest values are gone. `Table` applies the same per column across
rows. Correctness of the incremental bookkeeping is verified against a
full re-scan of the live values in `infer_test.go`.

```go
c := infer.NewColumn()
c.Add("true")
c.Add("7")
fmt.Println(c.Kind()) // int
c.Remove("7")
fmt.Println(c.Kind()) // bool
```
