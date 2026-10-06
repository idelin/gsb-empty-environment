# dectree

Optimal decision trees for identifying an unknown object drawn from at
most eight candidates, using deterministic tests that each charge an
integer fee, under a per-path cost budget.

## Model

- `Instance` lists `Candidates` (each with a rational prior
  `*big.Rat`), `Tests` (each with an integer `Cost` and a deterministic
  `Outcomes` table mapping every candidate ID to its result label), and
  a `Budget` bounding the total fee spent along any single path.
- `Solve` returns a `Strategy`: a pure decision tree. The test at each
  node depends only on outcomes observed so far — never on the hidden
  truth.

## Optimality

`Solve` minimises, lexicographically:

1. the worst-case total cost over all root-to-leaf paths, then
2. the expected total cost under the candidate probabilities
   (computed exactly with `math/big.Rat`).

A node becomes a leaf only when no affordable test can split its
candidate set further; such leaves report the remaining
indistinguishable candidates together with the `Evidence` — the
sequence of test outcomes that led there.

## Verification

`ExhaustiveVerify(inst, strat)` independently confirms a strategy by

1. checking structural validity (affordable, splitting tests; complete
   branches; justified leaves; correct evidence),
2. simulating every candidate as the hidden truth (`Simulate`) to show
   each reaches a containing leaf within budget — the tree shape itself
   guarantees tests are chosen from observations only, and
3. recursively enumerating all strategy trees as Pareto frontiers of
   (worst, expected) cost per subproblem, confirming `strat` attains
   the lexicographic optimum.

## Example

```go
inst := &dectree.Instance{
    Candidates: []dectree.Candidate{
        {ID: "X", Prob: big.NewRat(1, 2)},
        {ID: "Y", Prob: big.NewRat(1, 4)},
        {ID: "Z", Prob: big.NewRat(1, 4)},
    },
    Tests: []dectree.Test{
        {Name: "tX", Cost: 1, Outcomes: map[string]string{"X": "yes", "Y": "no", "Z": "no"}},
        {Name: "tY", Cost: 1, Outcomes: map[string]string{"X": "no", "Y": "yes", "Z": "no"}},
    },
    Budget: 2,
}
root, _ := dectree.Solve(inst)          // tests tX first: worst 2, expected 3/2
fmt.Print(root)
err := dectree.ExhaustiveVerify(inst, root)
```
