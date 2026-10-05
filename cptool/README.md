# cptool — critical path analysis under interval-uncertain clocks

Analyzes task lifecycles (ready → start → end), resource queueing, and
dependency-completion events where every clock reading carries a known error
interval. It computes the **feasible range of the total critical-path
duration**, emits **one realizable shortest and one realizable longest
explanation**, attributes elapsed time to **compute-slow vs. queue-slow**,
and **locates contradictory time constraints** instead of silently
subtracting them into negative durations.

## Usage

```
go build -o cptool .
./cptool examples/pipeline.json      # human-readable report
./cptool -json examples/pipeline.json
./cptool examples/contradiction.json # exits 1, prints the conflict chain
go test ./...
```

## Input model

```json
{
  "origin": { "lo": 0, "hi": 0 },
  "tasks": [
    { "id": "compile",
      "ready": { "lo": 2, "hi": 4.5 },
      "start": { "lo": 3, "hi": 6 },
      "end":   { "lo": 9, "hi": 13 },
      "deps":  ["fetch"] }
  ]
}
```

- `origin`: the project clock-zero event (may itself be uncertain).
- `ready`: dependencies satisfied / queued for a resource.
- `start`: resource acquired, execution begins.
- `end`: execution finished (also the dependency-completion event for
  downstream tasks).
- Each timestamp is only known to lie inside `[lo, hi]`.

Derived quantities: `wait = start - ready` (queueing), `run = end - start`
(compute), `makespan = max(end) - origin`.

## Method

Every event is a variable constrained to its observation interval; every
lifecycle/dependency relation is a difference constraint `t_v >= t_u`.
This yields a system of difference constraints:

- **Feasibility + shortest explanation** — a forward longest-path closure
  (Bellman-Ford style) from the observation lower bounds gives the
  componentwise-minimal feasible assignment. The origin is first pushed to
  its latest feasible value, since durations are measured as `T - origin`.
- **Longest explanation** — propagating observed upper bounds backwards
  along precedence edges yields the componentwise-maximal feasible
  assignment (it is feasible because `ub[u] <= ub[v]` holds along every
  edge). The makespan is the largest feasible task end; the origin is moved
  to its earliest observed value.
- **Critical chain & attribution** — the chain is traced backwards from the
  makespan along structural edges, always following the latest predecessor.
  Elapsed time telescopes to the makespan along any such path; hops on
  `start -> end` edges are attributed to *compute*, hops on
  `ready -> start`, dependency, and lead-in edges to *queue*.
- **Contradiction localization** — when the lower-bound closure exceeds an
  observed upper bound, the tool walks the predecessor chain and prints the
  exact sequence of observations and precedence constraints that collide
  (e.g. `build.start >= 9 -> build.end <= 5`). No negative durations are
  ever fabricated by naive subtraction.

Complexity is `O(n * m)` Bellman-Ford relaxations, with `n = 3 * |tasks| + 2`
event nodes.

## Limitations / extensions

- Resource contention is modeled through observed `ready -> start` waits;
  adding mutual-exclusion (disjunctive) constraints for shared resources
  would require a disjunctive scheduler on top.
- All precedence constraints currently have zero lag; the solver already
  supports weighted edges if minimum-lag constraints are added later.
