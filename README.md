# Tree Quota Allocator

Integer quota allocation over a tree of budget constraints. Pure Python 3.11
standard library (`fractions`, `math`, `enum`, `dataclasses`, `unittest`);
no external solver.

## Model

- **Branch nodes** carry `lower` / `upper` bounds on their subtree total.
- **Leaf nodes** carry a `demand` (cap) and a `weight` for proportional
  splits.
- Constraint data are exact numbers: `int`, `fractions.Fraction`, or strings
  like `"7/2"`. Floats are rejected to keep arithmetic exact.
- Leaf allocations are integers. Effective integer bounds are derived by
  rounding: `ceil(lower)`, `floor(upper)`, `floor(demand)`.
- **Conservation**: every branch total equals the sum of its children's
  totals, hence the sum of leaf allocations in its subtree.

## Statuses (kept separate)

| Status | Meaning |
| --- | --- |
| `OPTIMAL` | Root bounds exist and every leaf demand is fully satisfied. |
| `FEASIBLE` | All bounds hold, but the budget binds; some demand is unmet. |
| `BUDGET_UNDECIDED` | Root has no bounds; full demand is allocated and reported. |
| `INFEASIBLE` | Bounds conflict; result carries the minimal conflicting subtree. |

## Algorithm

1. **Bottom-up analysis** (`_analyze`): compute each subtree's effective
   integer range `[lo, hi]` from children ranges intersected with own bounds.
   The first node (post-order) with `lo > hi` raises a conflict; since its
   proper subtrees are consistent, the reported subtree is **minimal**.
2. **Target selection**: the root target is its effective upper bound
   (capped by total demand). The status is derived from whether the root
   carries bounds and whether all demands are met.
3. **Top-down distribution** (`_split`): each node splits its target among
   children — lower bounds first, then exact rational shares proportional to
   subtree weights, floored, with leftover units handed out by **largest
   fractional remainder**. Equal remainders are resolved by ascending
   **stable node id**. Caps are respected; freed units are redistributed
   among children with remaining headroom.

## Usage

```python
from allocator import Node, allocate, verify

root = Node.branch("hq", [
    Node.branch("eng", [
        Node.leaf("eng.backend", demand=30, weight=3),
        Node.leaf("eng.frontend", demand=20, weight=2),
    ], upper=40),
    Node.leaf("ops", demand=25, weight=2),
], upper=60)

res = allocate(root)
print(res.status.value)   # 'feasible'
print(res.allocations)    # leaf id -> int quota
print(res.totals)         # every node id -> subtree total
assert verify(root, res) == []  # conservation + bounds check
```

## Run

```sh
python3 demo.py            # runnable sample covering all four statuses
python3 -m unittest -v     # test suite
```

## Files

- `allocator.py` — model, solver, verifier.
- `test_allocator.py` — unittest suite (rounding, ties, rationals, statuses,
  conflicts, conservation sweeps).
- `demo.py` — runnable sample.
