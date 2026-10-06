# pdp

Go library for small capacitated pickup-and-delivery problems (PDP) with a
single vehicle that starts and ends at the same depot.

## Static problem

- Up to `MaxRequests` (6) pickup/delivery pairs, each with a weight.
- Complete, non-negative distance matrix; depot is both start and end.
- Pickup precedes delivery; deliveries release capacity; the load never
  exceeds the vehicle capacity.
- `pdp.Solve` enumerates every legal pickup/delivery order (DFS with
  precedence and capacity pruning) and returns the jointly optimal route —
  never a composition of independent per-request shortest paths.
- Ties in cost are broken by the lexicographically smallest sequence of
  visited point ids (start, every stop node, final depot).
- The returned `Solution` carries the full load trajectory: `Loads[0]` is
  the initial load, `Loads[i+1]` the load right after stop `i`, and each
  `Stop` records its own `LoadAfter` as evidence.

```go
in := pdp.Instance{Depot: 0, Capacity: 5, Dist: dist}
reqs := []pdp.Request{{ID: 1, Pickup: 1, Delivery: 4, Weight: 2}}
sol, err := pdp.Solve(in, reqs)
```

## Online cancellations

`pdp.Simulator` drives the vehicle along the plan while cancellation events
arrive. The executed prefix is fixed forever (`Advance`); after every event
the optimal remaining route is recomputed (`Plan`).

- Cancel a **pending** request: it is voided and both legs disappear.
- Cancel an **onboard** request (at the pickup moment or en route): it must
  be dropped at the given return point and keeps occupying capacity until
  then (`ReturnEvent` stop).
- Cancel at the **delivery moment** or later: the delivery is already part
  of the fixed prefix, so the cancellation is a no-op.

Every action appends a `JournalEntry` with the per-request status snapshot
plus load evidence (position, load, cost of the fixed prefix).

```go
sim, _ := pdp.NewSimulator(in, reqs)
sim.Advance(2)          // execute the first two planned stops
sim.Cancel(1, 6)        // request 1 must now be returned to point 6
plan := sim.Plan()      // optimal remaining route after the event
```

## Tests

`go test ./...` cross-checks the solver against an independent
full-permutation brute-force enumerator on random instances, verifies the
load trajectory, proves the joint optimum beats independent per-request
tours, checks the tie-break rule, and replays cancellation scenarios
(before pickup, at the pickup moment, en route, at the delivery moment)
validating every replanned route against brute force.
