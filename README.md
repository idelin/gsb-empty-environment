# Fault-Domain-Aware Replica Placement

Selects nodes in distinct fault domains (racks) for object replicas, respects
node capacity and explicit object anti-affinity, and minimises total
transmission cost. Pure Python 3.11 standard library — no external solver.
All constraint data are synthetic integers or rationals (`fractions.Fraction`,
exact arithmetic; strings like `"3/2"` accepted).

## Rules enforced

- Rack isolation: two nodes in the **same rack do not** count as isolated
  fault domains; an object's replicas must span distinct racks.
- A replica of an object is never counted twice on the same node.
- Node capacity: sum of object sizes on a node never exceeds its capacity.
- Anti-affinity: declared object pairs never share a node.
- Migration plans add new replicas before evicting old ones, so the minimum
  replica count holds at **every** step.

## Result statuses (strictly separated)

| Status | Meaning |
|---|---|
| `OPTIMAL` | search completed; placement provably optimal (and within budget if given) |
| `FEASIBLE` | valid placement within budget found; search budget ran out before proof |
| `BUDGET_UNDECIDED` | no within-budget placement found, search budget exhausted — undecided |
| `BUDGET_EXCEEDED` | search completed; optimum provably exceeds the budget |
| `INFEASIBLE` | no valid placement exists; `diagnostics` explain the capacity shortage |

## Usage

```python
from placement import Problem, solve, build_migration_plan

problem = Problem.build(
    nodes=[("n1", "r1", 10), ("n2", "r2", 10)],
    objects=[("A", 4, 2)],
    costs={("A", "n1"): 5, ("A", "n2"): "3/2"},
    anti_affinity=[],
)
result = solve(problem, budget=20, node_limit=200_000)
print(result.status, result.cost, result.placement, result.diagnostics)
```

## Run

```
python3.11 run_sample.py            # end-to-end sample scenarios
python3.11 -m unittest discover -s tests -v
```
