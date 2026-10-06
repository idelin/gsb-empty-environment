# flowbounds

Go library for the **feasible flow with edge bounds** problem on directed
networks.

## Model

- Each edge has an integer flow range `[Lower, Upper]`; parallel edges are
  supported.
- Each node has a net-inflow demand: `inflow(v) - outflow(v) = Demand[v]`,
  and demands must sum to zero.
- Self-loops (`From == To`) cancel out of node conservation and are only
  constrained by their own range.
- Isolated nodes are feasible iff their demand is zero.

## API

- `Solve(*Network) (Result, error)` — returns either a feasible flow (one
  value per edge, input order) or a `CutCertificate`: a node set `U` with
  `demand(U) > upperIn(U) - lowerOut(U)`, which proves infeasibility.
- `CheckFlow(*Network, []int64) error` — independently verifies edge
  ranges and node conservation.
- `CheckCertificate(*Network, *CutCertificate) error` — independently
  verifies an infeasibility certificate.
- `BruteForceFeasible(*Network, maxCombos int64) (bool, error)` —
  exhaustively enumerates all in-range flows; for cross-checking small
  instances only.

## Method

Lower bounds are committed up front, turning the instance into a
circulation-with-demands problem on residual capacities `Upper - Lower`,
which is solved with a Dinic max flow between a super source and sink.
When the max flow falls short, the sink side of the minimum cut is
exactly a violated set `U` (Gale–Hoffman criterion), returned as the
certificate.

## Test

```sh
go test ./...
```

Randomized tests cross-validate `Solve` against `BruteForceFeasible` on
thousands of small instances containing parallel edges, self-loops and
isolated nodes, and run both independent checkers on every outcome.
