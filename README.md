# policytree

A Go library that solves finite control decision problems with probes.

## Model

- Up to **16 possible worlds**, up to **8 terminal actions**.
- Each action gives, per world, an **integer loss** and an **observable symbol**.
- Before acting you may run **at most two probes**; each probe has an
  **integer cost** and reveals a world-dependent observation symbol.
- Probes may be chosen **adaptively** based on earlier observations.
- The policy must be **non-clairvoyant**: identical observation histories
  must lead to the same action. This is enforced structurally by
  representing policies as trees keyed on observation symbols.

## Objective

Minimize the **worst-case total loss** (probe costs + action loss) over all
worlds; break ties by the **expected total loss** under rational world
probabilities (`math/big.Rat`, exact arithmetic).

## API

- `(*Problem).Solve()` — optimal policy tree via memoized DP over
  information states (world set, remaining probe budget, used probes).
  Each state keeps a Pareto frontier of `(worst, expected)` pairs, because
  the lexicographic objective does not decompose greedily per state.
- `(*Problem).TreeString` — renders the policy tree.
- `(*Problem).ReplayAll` / `ReplayString` — per-world traces with costs.
- `(*Problem).CheckPolicy` — validates budget, coverage, and
  non-clairvoyance of any tree.
- `(*Problem).BruteForce()` — enumerates all feasible policy trees on
  small instances and returns the optimal objective plus the tree count;
  used to cross-check the solver.

## Run

```sh
go test ./...    # solver vs. exhaustive enumeration on random instances
go run ./cmd/demo
```
