# flowsolve

Go library for the integer feasible-flow problem on directed networks:

- each edge has integer lower/upper bounds `Lo <= f <= Hi` (parallel edges allowed);
- each node has a net-inflow demand, `inflow - outflow == Demand[v]`, demands sum to zero;
- self-loops are only constrained by their own bounds (they cancel in conservation);
- isolated nodes are allowed (feasible iff their demand is zero).

## API

- `Solve(*Network) (flow []int64, cert *Certificate, err error)` — returns a feasible
  flow (one value per edge, input order) or a cut certificate proving infeasibility.
  Errors: `ErrDemandSum`, `ErrBadBounds` (`Lo > Hi`), `ErrBadNode`.
- `VerifyFlow(*Network, []int64) error` — independently checks every edge bound and
  node conservation.
- `VerifyCertificate(*Network, *Certificate) error` — independently checks the cut
  inequality `Demand(X) < LoIn(X) - HiOut(X)`.
- `BruteForce(*Network, maxCombos int64) (bool, error)` — exhaustive enumeration over
  small capacities, used to cross-check `Solve`.

## Method

`Solve` substitutes `f = Lo + g` and runs Dinic max-flow on the standard auxiliary
graph (super source feeds nodes with negative residual need, super sink drains nodes
with positive need). The instance is feasible iff every source edge saturates;
otherwise the residual reachable set is a violating cut, returned as the certificate.

Correctness is validated by `TestRandomAgainstBruteForce`: 2000 random small networks
(with parallel edges, self-loops, isolated nodes) are solved both by `Solve` and by
`BruteForce`, and every returned flow/certificate is independently verified.

Run tests with:

```sh
GOCACHE=$(mktemp -d) go test ./...
```
