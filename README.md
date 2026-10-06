# validate

Rule-set validation for map-shaped Go data, where field rules form a
dependency DAG.

## Semantics

- **Dependency DAG**: each `Rule` declares `DependsOn` rule IDs. Rules run in
  topological order (deterministic, ties broken by rule ID).
- **Skip, don't mislead**: when a base rule fails (e.g. a type check), every
  rule depending on it is reported in `Result.Skipped` instead of producing
  cascading, misleading errors. Independent rules still run and their errors
  are collected.
- **Cycles rejected**: `NewRuleSet` refuses rule sets with dependency cycles
  (also duplicate IDs, unknown dependencies, empty IDs, nil checks).
- **Stable ordering**: `Result.Errors` and `Result.Skipped` are sorted by
  field path, then rule ID.
- **Batch isolation**: `ValidateBatch` evaluates each item with fresh state;
  results equal per-item `Validate` calls.

## Example

```go
rs, _ := validate.NewRuleSet(
    validate.Rule{ID: "age-type", Field: "age", Check: validate.IsInt()},
    validate.Rule{ID: "age-min", Field: "age", DependsOn: []string{"age-type"}, Check: validate.Min(0)},
    validate.Rule{ID: "name", Field: "name", Check: validate.Required()},
)
res := rs.Validate(map[string]interface{}{"age": "oops"})
// res.Errors:  [age/age-type, name/name]
// res.Skipped: [age/age-min]  (dependency "age-type" did not pass)
```

Built-in checks: `Required`, `Optional`, `IsString`, `IsBool`, `IsInt`,
`IsNumber`, `Min`, `Max`, `LenMin`, `LenMax`, `OneOf`.

## Testing

`model_test.go` cross-checks the library against an independent per-rule
fixpoint interpreter over randomized DAGs and inputs, covering missing
fields, conditional validation and batch isolation.
