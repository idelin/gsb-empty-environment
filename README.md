# mcf — exact min-cost flow for small transportation graphs

Go library + CLI for exact minimum-cost flow on small transportation
graphs (at most 8 nodes / 12 edges): integer flows, finite capacities,
**negative costs allowed**.

## Guarantees

- **Exact, not greedy.** `Solve` runs a max-flow feasibility phase and
  then cancels *negative-cost residual cycles* (SPFA/Bellman-Ford
  detection) until none remain. A shortest-augmenting-path-only solver
  can strand negative cycles in the residual graph and return a
  non-optimal flow; this one cannot — absence of negative residual
  cycles is the optimality certificate.
- **Infeasible is reported, not approximated.** If the supplies cannot
  be routed within `[Lo, Cap]`, the result is `Infeasible` with no
  flow attached.
- **Independently verified.** `Exhaustive` enumerates every integer
  flow vector in the capacity box and checks conservation directly.
  `mcf_test.go` cross-checks `Solve` against `Exhaustive` on hundreds
  of random instances (including negative costs and lower bounds);
  the CLI re-verifies on demand with `"verify": true`.
- **Per-edge output.** Results carry the full per-edge flow vector and
  the total cost.

## Staged flow commitments

`SolveStaged` re-optimizes when new demand arrives after part of the
plan already executed:

- `Executed` flow on an edge is sunk — re-optimization may never
  reduce it; only the unexecuted remainder moves, within remaining
  capacity.
- A `Canceled` edge keeps its executed amount (it is frozen at
  `Executed`, never erased) and offers no further capacity.
- Output is the optimal new total plan plus `IncrementalCost`
  (total minus the sunk executed cost).
- **Infeasibility comes with a cut certificate**: the sink-side node
  set of a min cut, its net demand, the capacity crossing into it,
  and the positive deficit.
- **Resumable frontier.** The search enumerates free-edge flow
  vectors in lexicographic order, so the frontier is a single vector
  checkpointed to `FrontierPath` (JSON, written atomically). A killed
  run continues with `"resume": true`.
- **Budget termination returns bounds, not claims.** If `Budget`
  (number of evaluated vectors) runs out, the result is
  `BudgetExhausted` with a relaxation-based `Lower` bound and, once
  any feasible plan was seen, an incumbent `Upper` bound — never an
  optimality claim.

## CLI

```
go build ./cmd/mcf
./cmd/mcf instance.json        # or pipe JSON on stdin
```

```json
{
  "nodes": 4,
  "edges": [{"from":0,"to":1,"lo":0,"cap":5,"cost":2,
             "executed":1,"canceled":false}],
  "supply": [3,0,0,-3],
  "source": 0, "sink": 3, "value": 3,   // alternative to "supply"
  "verify": true,
  "staged": true,
  "budget": 100000,
  "frontier": "frontier.json",
  "resume": false
}
```

The report contains the base `Solve` result (`status`, per-edge
`flow`, `cost`, `verified`) and, when `"staged": true`, a `staged`
block with the committed-flow result: optimal `flow`/`totalCost`/
`incrementalCost`, or `lower`/`upper` bounds on budget exhaustion, or
a `cut` infeasibility certificate.

## Test

```
go test ./...
```
