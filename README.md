# psdcomp — exact PSD matrix completion over the rationals

Solves positive-semidefinite completion for symmetric matrices of order
n ≤ 4: some entries are fixed rationals, each remaining upper-triangular
entry picks from a finite rational candidate set (≤ 1,000,000 total
combinations). The solver finds the completion that

1. makes the matrix **positive semidefinite**,
2. **minimizes the trace**,
3. breaks ties **lexicographically on the upper triangle**
   (0,0),(0,1),…,(0,n−1),(1,1),…,(n−1,n−1).

All arithmetic is exact (`math/big.Rat`); no floating point anywhere.

## Correctness criterion

A symmetric matrix is PSD iff **all** principal minors are nonnegative
(Sylvester's criterion for semidefiniteness). Checking only the diagonal
and the determinant is *not* sufficient — e.g.

    [1 2 2]
    [2 1 2]   diag > 0, det = 5 > 0, but det(A[{0,1}]) = -3 < 0  ⇒  not PSD
    [2 2 1]

The solver checks all 2ⁿ − 1 principal minors (15 for n = 4).

## Layout

- `matrix.go` — exact rational matrix type; determinants via two
  independent algorithms (fraction-free Bareiss and rational Gaussian
  elimination).
- `psd.go` — PSD predicates: `IsPSDMinors` (all principal minors ≥ 0)
  and the independent `IsPSDElimination` (exact LDL-style symmetric
  elimination with zero-pivot handling).
- `problem.go` — problem model and validation (n ≤ 4, ≤ 1e6 combos).
- `solve.go` — DFS solver with two sound pruning rules, each emitting
  per-branch evidence via `Solver.OnPrune`:
  - `PruneMinorViolation`: a fully determined principal submatrix has
    negative determinant (witness: the minor and its value);
  - `PruneTraceBound`: partial trace + minimal remaining diagonal trace
    exceeds the incumbent (witness: bound vs. incumbent).
  The result carries a certificate listing every principal minor ≥ 0.
- `brute.go` — pruning-free `BruteForce` using the elimination-based
  test, plus `VerifyResult`, which re-checks the certificate with both
  determinant algorithms, re-runs the elimination PSD test, validates
  structural conformance, and confirms agreement with brute force.
- `cmd/psdcomp` — JSON CLI.

## CLI

```sh
go run ./cmd/psdcomp -input problem.json -evidence prunes.jsonl
```

Input:

```json
{
  "n": 3,
  "fixed":     [{"i":0,"j":1,"value":"2"}],
  "variables": [{"i":0,"j":0,"candidates":["0","1","2","3"]}]
}
```

Output: optimal matrix, trace, the all-principal-minors certificate,
search statistics, and the verification verdict. `-evidence` writes one
JSON line per pruned branch (prefix, reason, witness). `-describe`
prints a human-readable report; `-verify=false` skips the independent
verification.

## Tests

```sh
go test ./...
```

Covers: agreement of the two determinant algorithms and the two PSD
predicates on random matrices, the diagonal+determinant counterexample,
hand-solved instances (lex tie-break, trace minimization, infeasibility
evidence), prune-evidence consistency, and randomized Solve-vs-BruteForce
cross-validation.
