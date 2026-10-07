# policytree

A Go library for solving small decision problems under partial observability
with an optimal policy tree.

## Model

- Up to 16 possible worlds, each with a rational probability (given as
  weights like `"1/3"`; they are normalized internally).
- Up to 8 terminal actions; action `a` in world `w` incurs an integer
  `loss[a][w]` and reveals an observable `symbol[a][w]` after the decision
  (so symbols describe what you see once you act, and cannot be used to
  decide).
- Any number of probes; probe `p` costs an integer `cost[p]` (paid once)
  and reveals `outcome[p][w]` in world `w`.
- At most 2 probes may be taken before acting (`maxProbes`, 0-2). Probes
  are adaptive: the second probe may depend on the first probe's outcome.
- Non-anticipation: the policy is a tree that branches only on observed
  symbols, so identical observation histories always yield the same action.

## Objective

Minimize the worst-case total loss over worlds (probe costs along the path
plus the final action loss). Ties are broken by the expected total loss
under the world probabilities, computed exactly with `math/big.Rat`.

## Usage

    go run ./cmd/policysolve -brute examples/example.json

prints the optimal policy tree, its cost, a non-anticipation check, a
per-world replay, and (with `-brute`) an exhaustive-enumeration cross-check.

Input is JSON (see `examples/`): worlds with rational probabilities, actions
with per-world losses and symbols, probes with per-world outcomes and a
cost, and `maxProbes`.

## Library API

- `Load(io.Reader) (*Problem, error)` parses and validates a JSON problem.
- `Solve(p) (Cost, *Node, error)` returns the optimal cost and policy tree
  via memoized dynamic programming over (information set, probes left).
  Past probe costs are sunk, so the optimal subtree depends only on the set
  of worlds consistent with the observations and the remaining probe budget.
- `Node.String(p)` renders the tree.
- `Replay(p, tree)` walks each world through the tree: probes, outcomes,
  action, symbol, and total loss.
- `VerifyNonAnticipation(p, tree)` checks the policy is a well-defined
  function of the observation history and respects the probe budget.
- `BruteForce(p)` exhaustively enumerates every valid policy tree and scores
  each independently (`EvalTree`), cross-checking the dynamic program on
  small instances.

## Tests

    go test ./...

Tests cover hand-computed optima, exact rational tie-breaking, and a
randomized cross-check of `Solve` against `BruteForce` plus replay
consistency (replay worst/expectation reproduce the solver's cost exactly).
