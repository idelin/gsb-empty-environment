# psdcomplete

Exact semidefinite matrix completion for symmetric matrices up to 4x4.

Given a symmetric matrix in which some entries are fixed rationals and each
remaining entry must be chosen from its own finite set of rational
candidates (at most 1,000,000 combinations in total), the library finds the
assignment that

1. makes the matrix **positive semidefinite**,
2. minimizes the **trace**,
3. breaks ties by **lexicographic order of the upper triangle** (row-major).

All arithmetic is exact (`math/big.Rat`); there is no floating-point error
anywhere in the decision or the optimization.

## PSD criterion — not just diagonal + determinant

A symmetric matrix is PSD iff **all** of its `2^n - 1` principal minors are
non-negative. Checking only the diagonal entries and the full determinant is
insufficient: `diag([1,x],[x,1], [1,y],[y,1])` with `x = y = 2` has positive
diagonal and determinant `9 >= 0`, yet the principal minor on `{1,2}` is
`-3`. The solver checks every principal minor, and the returned certificate
lists them all (see `TestDetAndDiagInsufficient`).

## Solvers

- `Solve(spec, opts)` — depth-first branch and bound. Branches are cut as
  soon as a fully assigned principal submatrix has a negative determinant,
  when the reachable trace lower bound exceeds the incumbent, or when only a
  lexicographically larger tie remains. Every cut produces a structured
  `PruneEvent` (the per-branch pruning evidence).
- `SolveBrute(spec)` — independent exhaustive enumerator with no pruning,
  for cross-checking small candidate sets.
- `VerifyLDL(m)` — independent PSD verifier using exact symmetric Gaussian
  elimination (LDL^T); shares no code with the minor computation. `Solve`
  cross-checks its own certificate with it and fails loudly on disagreement.

## Output

`Result` contains the optimal matrix and trace, a `Certificate` (all
principal minors with exact values + the LDL elimination record), the prune
log (`PruneLog`, capped by `Options.MaxPruneLog`; `PruneTotal` is the full
count), and search statistics. `Result.String()` renders everything in
human-readable form. Infeasible instances return `OK == false` plus the
pruning evidence showing why every branch fails.

## Example

```go
spec, _ := psdcomplete.NewSpec(2)
_ = spec.Fix(0, 0, psdcomplete.Rat(1))
_ = spec.Fix(1, 1, psdcomplete.Rat(1))
_ = spec.SetCandidates(0, 1, psdcomplete.Rat(-2), psdcomplete.Frac(-1, 2), psdcomplete.Rat(2))
res, _ := psdcomplete.Solve(spec, nil)
fmt.Print(res.String())
// a12 = -1/2, trace = 2, certificate lists det A[{1}], A[{2}], A[{1,2}]
```

## Tests

`go test ./...` runs unit tests plus randomized cross-checks: the pruning
solver against brute-force enumeration on hundreds of random instances, and
the minors criterion against exact LDL elimination on thousands of random
symmetric matrices.
