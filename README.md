# wafdecision

`wafdecision` compiles a Boolean WAF rule into a decision DAG that minimizes
the worst-case total detector cost while preserving short-circuit semantics.

It is designed for request-path use in a Go release service: compilation is an
offline, one-time step; per-request evaluation is a pointer-free node walk
with no allocations beyond the stats map.

## Model

- A rule is a Boolean formula of `Leaf`, `Not`, `And`, `Or`.
- Each leaf is a pure detector with an integer `Cost` and a selectivity
  interval `[SelLo, SelHi]` (the probability the detector matches).
- `And`/`Or` short-circuit; children may be reordered, `Not` may not.
- A leaf runs at most once per request, even when the formula shares it
  across branches.
- A detector panic is recovered and converted into a deny verdict.
- At most 12 leaves per rule.

## Algorithm

1. The formula is converted to negation normal form (NNF), flattening nested
   connectives and folding constants.
2. A state is a partial truth assignment (`known`/`vals` bit masks). At most
   `3^n` states exist for `n <= 12`.
3. From each state, the *frontier* is the set of leaves that may legally be
   tested next under short-circuit semantics. Testing a leaf branches into two
   successor states.
4. A minimax DP picks the frontier leaf minimizing the worst-case additional
   cost:

   ```
   W(s) = 0                                              if the rule is decided
   W(s) = min over frontier leaves i of
            cost_i + max(W(s + i=false), W(s + i=true))
   ```

   Equal worst-case choices are broken by expected cost at selectivity
   midpoints, then by leaf identifier, giving deterministic output.
5. Equivalent test nodes (same leaf and successors) are interned, so shared
   sub-formulas produce a compact DAG rather than an expanded tree.

## Verification

`Graph.Verify` enumerates all `2^n` truth assignments and, for each, checks
the verdict against the original formula, the at-most-once contract, and the
worst-cost bound. It also evaluates the expected cost over every vertex of
the selectivity interval box to report robust `[ExpectedCostLo,
ExpectedCostHi]` bounds.

## Usage

```go
rule := wafdecision.OrOf(
    wafdecision.NewLeaf(0),
    wafdecision.AndOf(wafdecision.NewLeaf(1), wafdecision.NewLeaf(2)),
)
specs := []wafdecision.LeafSpec{
    {ID: 0, Cost: 2, SelLo: 0.30, SelHi: 0.40},
    {ID: 1, Cost: 25, SelLo: 0.05, SelHi: 0.15},
    {ID: 2, Cost: 8, SelLo: 0.60, SelHi: 0.80},
}
g, err := wafdecision.Compile(rule, specs)
if err != nil { /* handle */ }
if _, err := g.Verify(); err != nil { /* handle */ }

outcome := g.Decide(map[int]wafdecision.Detector{
    0: headerRegex,
    1: bodyParser,
    2: signatureScan,
})
// outcome.Verdict is wafdecision.Allow or wafdecision.Deny.
```

Use `Graph.Eval` in tests when panics should propagate; use `Graph.Decide` in
production for uniform deny-on-failure handling. `Graph.DOT` exports the DAG
for visualization.
