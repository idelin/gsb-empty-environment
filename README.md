# dectree

Go library that builds **optimal decision trees** for identifying an unknown
object among up to 8 candidates with deterministic, costly tests.

## Model

- Each **candidate** has a rational prior probability (`*big.Rat`; weights
  are normalized internally).
- Each **test** costs an integer fee and returns a deterministic outcome per
  candidate.
- A **strategy** is a pure decision tree: the test applied at each step
  depends only on the outcomes observed so far — never on the unknown true
  object. Every root-to-leaf path must stay within `Problem.Budget`.

## Optimization

`Solve` minimizes lexicographically:

1. **worst-case** total fee over all paths, then
2. **expected** total fee under the candidate probabilities.

It keeps a Pareto frontier of `(worst, expected)` pairs per subproblem
(candidate set x remaining budget), so the lexicographic optimum is exact.
Candidates that cannot be separated — by any test, or within the remaining
budget — are reported in `Solution.Ambiguous` together with the observations
(evidence) that led to them.

## Verification

`Verify(p, sol)` independently checks a strategy:

- structure: one test per node for every candidate reaching it (no peeking
  at the truth), all realized outcomes have branches;
- budget: no path spends more than `Budget`;
- identification: leaves are singletons or groups no affordable test can
  separate;
- honesty: claimed costs match a fresh simulation of every candidate;
- optimality: costs equal the optimum found by **recursive exhaustive
  enumeration of all strategy trees** (a separate, memo-free
  implementation in `enumerate.go`).

## Usage

```go
sol, err := dectree.Solve(problem)   // optimal strategy
err = dectree.Verify(problem, sol)   // independent check
cost, leaf, err := dectree.Simulate(problem, sol.Root, cand)
fmt.Print(sol)                       // renders the tree
```

Run the tests (includes a randomized cross-check of the memoized solver
against brute-force enumeration):

```sh
go test ./...
```
