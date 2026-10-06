# rulexplain

`rulexplain` explains boolean rule trees. Given a rule and the current
variable assignment, it finds a **minimal set of variables** whose pinned
values guarantee the evaluation outcome — the boolean conclusion *and*
whether evaluation errors — stays identical no matter how the remaining
variables range over their finite domains.

## Guarantees

- **Full enumeration, not path tracing.** Sufficiency and minimality are
  verified by enumerating every assignment of the unpinned variables
  (at most 12 finite-domain variables). A single short-circuited
  evaluation path is never treated as a sufficient explanation.
- **Error semantics preserved.** Rules may reference unknown inputs
  (`ErrUnknownInput`) and custom predicates may fail at runtime (e.g.
  division by zero). Short-circuiting `And`/`Or` can mask those errors;
  an explanation guarantees the masked/surfaced error behaviour too.
- **Minimal, deterministic ties.** The returned set has minimum size;
  ties are broken by the lexicographically smallest sorted variable
  names.
- **Versioned cache.** Explanations are cached against the rule version
  (bumped by `Rule.SetRoot`) and the domain signature. Shrinking a
  domain via `Explainer.SetDomains` invalidates the cache, so the
  explanation can be re-shortened.

## Example

```go
rule := rulexplain.NewRule(rulexplain.Or{
    rulexplain.Condition{Var: "x", Op: rulexplain.Eq, Const: 1},
    rulexplain.Condition{Var: "ghost", Op: rulexplain.Eq, Const: 1}, // unknown input
})
exp, _ := rulexplain.NewExplainer(rule, map[string][]int{"x": {0, 1}})

ex, _ := exp.Explain(rulexplain.Env{"x": 1})
// ex.Vars == ["x"], ex.Outcome == {Value: true, Errored: false}:
// x=1 short-circuits before the unknown input, so pinning x alone
// keeps the outcome true and error-free for every other assignment.

ok, _ := exp.Verify(rulexplain.Env{"x": 1}, ex.Vars)   // exhaustive check
min, _ := exp.IsMinimal(rulexplain.Env{"x": 1}, ex.Vars) // every proper subset fails
```

## Layout

- `rule.go` — rule tree nodes (`And`, `Or`, `Not`, `Condition`, `Pred`),
  short-circuit evaluation, versioned `Rule`.
- `explain.go` — `Explainer`: exhaustive outcome table, minimal-set
  search, `Verify`/`IsMinimal`, versioned cache.
