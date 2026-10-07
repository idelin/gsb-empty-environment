# validag

A small Go validation library where field rules form a dependency DAG.

## Model

- Each `Rule` has a unique `ID`, a dot-separated `Field` path, optional
  `Depends` (IDs of rules that must pass first), an optional `When`
  condition over the whole input, and a `Check` function.
- `NewRuleSet` rejects duplicate IDs, unknown dependencies, self-dependencies,
  and dependency cycles. Evaluation order is a deterministic topological
  sort (ties broken by field path, then rule ID).
- `Validate` runs rules in topological order. A rule is **skipped** — never
  reported as an error — when a dependency did not pass, its condition is
  unmet, or its field is missing and it declared `SkipWhenMissing`. A failed
  base rule (e.g. a type check) therefore silences only its dependents;
  independent rules still run and their errors are collected.
- `Result.Errors` is sorted stably by field path, then rule ID.
- `Result.Traces` / `Result.Explain()` expose the per-rule explanation
  model: every rule is `PASS`, `FAIL` (with the error), or `SKIP` (with the
  reason), in evaluation order.
- `ValidateBatch` validates each element with fresh state and returns one
  `Result` per input, aligned by index — no cross-element leakage.

## Example

```go
rs, _ := validag.NewRuleSet(
    validag.Required("user.email", "email.required"),
    validag.IsString("user.email", "email.type", "email.required"),
    validag.MinLen("user.email", "email.min", 3, "email.type"),
)
res := rs.Validate(map[string]interface{}{})
// Errors: only email.required.
// Traces: email.type and email.min are SKIP (dependency did not pass).
fmt.Print(res.Explain())
```

## Test

```sh
go test ./...
```
