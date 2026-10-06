# markov

Go library for exact analysis of finite Markov chains with rational
transition probabilities (`math/big.Rat`, no floating-point error).

Given a target absorbing set `A`, it computes for every start state `i`:

- `HitProbability` — the exact probability `h_i` of ever reaching `A`;
- `ConditionalExpectedSteps` — `E_i[T | T < ∞]`, the expected number of
  steps to reach `A` conditioned on ever reaching it.

The two degenerate cases are reported distinctly:

- `h_i = 0` — `A` is **never reached**; the conditional expectation is
  undefined and returned as `nil`;
- `0 < h_i < 1` — the chain may never arrive, so the **unconditional**
  expectation `E_i[T]` is infinite (`UnconditionalExpectedSteps`
  reports `infinite = true`), while the conditional expectation, which
  is always finite in a finite chain, is still given exactly.

`Analysis.ClosedClasses` lists the closed communicating classes
disjoint from `A`; states trapped in them are exactly the ones that can
never reach `A`.

## Usage

```go
c, err := markov.ParseChain([][]string{
    {"1/2", "1/2", "0"},
    {"1/2", "0", "1/2"},
    {"0", "0", "1"},
})
an, err := c.Analyze([]int{2}) // absorbing set = state 2
for _, r := range an.Results {
    fmt.Println(r.State, r.HitProbability, r.ConditionalExpectedSteps)
}
```

`NewChain` validates that the matrix is square, every entry lies in
`[0,1]`, and every row sums to exactly 1; `ParseChain` accepts fraction
or decimal strings.

## Method

Hitting probabilities solve `h_i = Σ_j p_ij h_j` with `h = 1` on `A`.
When closed non-absorbing classes exist, `I − Q` is singular and the
system has many solutions; the hitting probability is the **minimal**
non-negative one. The library obtains it by reverse-BFS reachability:
states that cannot reach `A` get `h = 0`, and the reduced system on the
remaining transient states has a nonsingular `I − Q`, solved exactly by
Gauss–Jordan elimination over the rationals.

For expectations, `g_i = E_i[T·1{T<∞}]` solves `g_i = h_i + Σ_j p_ij g_j`
with the same matrix, and `E_i[T | T<∞] = g_i / h_i`.

Closed classes are Tarjan SCCs of the transition graph with no outgoing
edges, disjoint from `A`.

## Tests

`go test ./...` runs hand-computed small chains (certain absorption, a
closed two-state class, a partial-hit chain, a non-absorbing target)
plus a substitution check that plugs the computed `h` and `g` back into
the defining linear equations on every state.
