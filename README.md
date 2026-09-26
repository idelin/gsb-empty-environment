# Asymmetric CVRPTW on Synthetic Exact Data

Capacitated vehicle routing with time windows over a synthetic, possibly
**asymmetric** distance matrix. Pure Python 3.11 standard library — no
solver calls, no map integration. All constraint data are exact rationals
(`int` / `fractions.Fraction` / `(num, den)` tuples); floats are rejected.

## Layout

- `cvrptw/model.py` — `Customer`, `Problem`, `Status`, `Result`; exact-rational coercion.
- `cvrptw/exact.py` — branch-and-bound with node budget; proves `OPTIMAL` / `INFEASIBLE`, else `BUDGET_EXHAUSTED`.
- `cvrptw/heuristic.py` — cheapest-insertion heuristic; returns `FEASIBLE` or `NOT_FOUND`.
- `cvrptw/verify.py` — independent per-leg replay: arrival, wait, service start, load, horizon, exactly-once coverage.
- `main.py` — demo instance (asymmetric matrix, time windows, rationals).
- `tests/test_cvrptw.py` — 22 unittest cases.

## Status semantics (kept strictly separate)

| Status | Meaning |
|---|---|
| `OPTIMAL` | exact search finished; objective proven minimal |
| `FEASIBLE` | solution found (heuristic); optimality not claimed |
| `INFEASIBLE` | exact proof that no solution exists |
| `NOT_FOUND` | heuristic miss; says nothing about feasibility |
| `BUDGET_EXHAUSTED` | node budget spent; undecided (incumbent attached if one exists) |

`Result.objective` is always the *actual* total distance of the returned
routes — never a bound. `d(a,b) == d(b,a)` is never assumed; every customer
is visited exactly once (enforced by construction and re-checked by the
verifier).

## Run

```
python3.11 main.py
python3.11 -m unittest discover -s tests -v
```

## Sample run (`python3.11 main.py`)

```
== exact branch-and-bound ==
status: optimal
objective (actual total distance): 21
routes (customer indices): ((2, 0, 3), (4, 1))
nodes explored: 299
note: proven optimal by exhaustive search

per-leg arrival / load verification (exact solution):
vehicle 0: DEPOT -> C -> A -> D -> DEPOT
  route distance=12  max load=8/8
  leg from    to       depart  travel  arrive   wait   serve    load
    0 DEPOT   C             0       3       3      0       3       3
    1 C       A           7/2       3    13/2      0    13/2       6
    2 A       D          15/2       3    21/2      0    21/2       8
    3 D       DEPOT      23/2       3    29/2      0    29/2       8
vehicle 1: DEPOT -> E -> B -> DEPOT
  route distance=9  max load=8/8
  leg from    to       depart  travel  arrive   wait   serve    load
    0 DEPOT   E             0       4       4      0       4       4
    1 E       B             5       3       8      0       8       8
    2 B       DEPOT         9       2      11      0      11       8
total distance = 21
verification: OK

== greedy heuristic ==
status: feasible
objective (actual total distance): 21
routes (customer indices): ((2, 0, 3), (4, 1))
nodes explored: 0
note: insertion heuristic solution; optimality not claimed

== exact search with tiny node budget (5) ==
status: budget_exhausted
objective (actual total distance): None
routes (customer indices): ()
nodes explored: 6
note: node budget exhausted before any solution; feasibility undecided

== provably infeasible instance (demand > capacity) ==
status: infeasible
objective (actual total distance): None
routes (customer indices): ()
nodes explored: 0
note: customer X demand 9 exceeds vehicle capacity 8

```

## Test results (`python3.11 -m unittest discover -s tests -v`)

```
test_forward_direction_cheaper (test_cvrptw.AsymmetryTests.test_forward_direction_cheaper) ... ok
test_no_symmetry_assumption (test_cvrptw.AsymmetryTests.test_no_symmetry_assumption) ... ok
test_reverse_direction_cheaper (test_cvrptw.AsymmetryTests.test_reverse_direction_cheaper) ... ok
test_capacity_forces_second_vehicle (test_cvrptw.CapacityTests.test_capacity_forces_second_vehicle) ... ok
test_demand_above_capacity_infeasible (test_cvrptw.CapacityTests.test_demand_above_capacity_infeasible) ... ok
test_fleet_capacity_infeasible (test_cvrptw.CapacityTests.test_fleet_capacity_infeasible) ... ok
test_exact_rational_objective (test_cvrptw.RationalDataTests.test_exact_rational_objective) ... ok
test_floats_rejected (test_cvrptw.RationalDataTests.test_floats_rejected) ... ok
test_tuple_rationals_accepted (test_cvrptw.RationalDataTests.test_tuple_rationals_accepted) ... ok
test_budget_exhausted_is_undecided (test_cvrptw.StatusDistinctionTests.test_budget_exhausted_is_undecided) ... ok
test_budget_exhausted_may_carry_feasible_incumbent (test_cvrptw.StatusDistinctionTests.test_budget_exhausted_may_carry_feasible_incumbent) ... ok
test_heuristic_feasible_status (test_cvrptw.StatusDistinctionTests.test_heuristic_feasible_status) ... ok
test_heuristic_not_found_is_not_infeasible (test_cvrptw.StatusDistinctionTests.test_heuristic_not_found_is_not_infeasible) ... ok
test_order_forced_by_due_times (test_cvrptw.TimeWindowTests.test_order_forced_by_due_times) ... ok
test_unreachable_time_window_is_infeasible (test_cvrptw.TimeWindowTests.test_unreachable_time_window_is_infeasible) ... ok
test_waiting_is_accounted (test_cvrptw.TimeWindowTests.test_waiting_is_accounted) ... ok
test_capacity_violation_flagged (test_cvrptw.VerificationTests.test_capacity_violation_flagged) ... ok
test_clean_solution_verifies (test_cvrptw.VerificationTests.test_clean_solution_verifies) ... ok
test_duplicate_visit_flagged (test_cvrptw.VerificationTests.test_duplicate_visit_flagged) ... ok
test_missing_customer_flagged (test_cvrptw.VerificationTests.test_missing_customer_flagged) ... ok
test_route_distance_helper (test_cvrptw.VerificationTests.test_route_distance_helper) ... ok
test_time_window_violation_flagged (test_cvrptw.VerificationTests.test_time_window_violation_flagged) ... ok

----------------------------------------------------------------------
Ran 22 tests in 0.031s

OK
```
