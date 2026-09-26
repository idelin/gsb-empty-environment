# Synthetic Asymmetric CVRPTW Solver (stdlib only)

Capacitated vehicle routing with time windows on a **synthetic distance matrix**.
No map data, no external solver. Python 3.11+, standard library only.

- Distances may be **asymmetric**: `d(a, b)` is never assumed equal to `d(b, a)`.
- Every demand node is visited **exactly once** (enforced by search and re-checked by the verifier).
- Constraint data are **exact integers or rationals** (`fractions.Fraction`); objectives are exact.
- Per-segment **arrival / loading verification** is emitted for every returned solution.

## Status semantics (kept strictly distinct)

| status | meaning | objective |
|---|---|---|
| `optimal` | exact search exhausted; objective proven optimal | actual optimum |
| `feasible` | solution found, optimality **not** proven (budget exhausted with incumbent, or heuristic) | actual incumbent value |
| `infeasible` | **proven** no feasible solution exists | `None` |
| `budget_undecided` | node budget exhausted: no solution, no proof either way | `None` |
| `heuristic_not_found` | heuristic failed; feasibility **not** decided (≠ infeasible) | `None` |

## Files

- `cvrptw.py` — `Instance`, `solve_exact` (branch & bound with node budget),
  `solve_heuristic` (deterministic greedy), `verify_routes` (per-segment checker).
- `sample_run.py` — runnable samples covering every status.
- `test_cvrptw.py` — `unittest` suite (15 tests).

## Usage

```python
from cvrptw import Instance, solve_exact, solve_heuristic

inst = Instance.from_lists(
    dist=[[0, 2, 5], [2, 0, 1], [5, 10, 0]],   # asymmetric: d(1,2)=1, d(2,1)=10
    demand=[0, 4, 4], capacity=10,
    early=[0, 0, 0], late=[100, 100, 100], service=[0, 0, 0],
)
res = solve_exact(inst, node_limit=100_000)     # or solve_heuristic(inst)
print(res.status.value, res.objective, res.routes)
print("\n".join(res.report))                    # per-segment arrival/load verification
```

## Sample run (`python3 sample_run.py`, abridged)

```
== A1 exact solve (proven optimal) ==
status    : optimal
objective : 14
routes    : [[1, 2], [3, 4]]
per-segment verification:
  route 1: 0 -> 1 -> 2 -> 0
    0->1: depart 0, travel 2, arrive 2, wait 0, serve [2, 3], load 3/10  ok
    1->2: depart 3, travel 2, arrive 5, wait 0, serve [5, 7], load 7/10  ok
    2->0: depart 7, travel 2, arrive 9  ok
  route 2: 0 -> 3 -> 4 -> 0
    0->3: depart 0, travel 3, arrive 3, wait 0, serve [3, 4], load 2/10  ok
    3->4: depart 4, travel 2, arrive 6, wait 0, serve [6, 8], load 5/10  ok
    4->0: depart 8, travel 3, arrive 11  ok
  total distance = 14

== A2 exact solve, node budget = 1 (budget undecided) ==
status    : budget_undecided
objective : None

== A3 exact solve, node budget = 4 (feasible, optimality unproven) ==
status    : feasible
objective : 20
routes    : [[1, 2, 3], [4]]

== A4 heuristic solve (feasible, not proven) ==
status    : feasible
objective : 20

== B exact solve (infeasible: total demand > one vehicle) ==
status    : infeasible

== C1 exact solve (infeasible: window unreachable) ==
status    : infeasible
detail    : customer 1 unreachable within its window (earliest possible arrival 3 > latest 2)

== C2 heuristic solve (not found, distinct from infeasible) ==
status    : heuristic_not_found
detail    : greedy construction found no feasible solution; feasibility is NOT decided
```

## Test results (`python3 -m unittest test_cvrptw -v`)

```
Ran 15 tests in 0.014s
OK
```

Covers: hand-computed optima, asymmetry (`d(a,b) != d(b,a)`), visit-once,
exact rational objectives, capacity/window/vehicle-limit infeasibility proofs,
budget-undecided vs feasible-but-unproven, heuristic-not-found vs infeasible,
and verifier detection of duplicates, overloads and window misses.

## Notes & limits

- Exact search is exponential; it is meant for small synthetic instances. Use
  `node_limit` to bound effort — the status then tells you precisely what is
  (and is not) known.
- Vehicle symmetry is broken by requiring increasing first-customer ids across
  routes, which prunes duplicate route orderings without losing optimality.
