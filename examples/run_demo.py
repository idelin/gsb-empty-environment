"""Synthetic-data demo for countadjust.  Run:  python3.11 examples/run_demo.py

All numbers are synthetic.  Nothing here is a formal privacy guarantee.
"""

import os, sys
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from countadjust import CountDAG, Status, WLSSolver, enumerate_active_sets


def show(title, result, names):
    print(f"== {title} ==")
    print(f"  status     : {result.status.value}")
    if result.x is not None:
        adj = {n: str(v) for n, v in zip(names, result.x)}
        print(f"  adjusted x : {adj}")
        print(f"  objective  : {result.objective}")
        print(f"  iterations : {result.iterations}")
        cert = result.certificate
        print(f"  KKT valid  : {cert.is_valid()} "
              f"(max |stationarity| = {max(map(abs, cert.stationarity_residual))}, "
              f"max |primal| = {max(map(abs, cert.primal_residual))})")
    if result.infeasibility_certificate is not None:
        print(f"  Farkas mu  : {result.infeasibility_certificate}")
    if result.singularity_direction is not None:
        print(f"  null dir d : {result.singularity_direction} "
              f"(undetermined: {result.undetermined_variables})")
    print()


# 1. Two-leaf tree: leaves must sum to the root.
m = CountDAG()
m.add_node("root", observed=10, weight=1)
m.add_node("a", observed=3, weight=1)
m.add_node("b", observed=5, weight=1)
m.add_parent_equals_children("root", ["a", "b"])
r = WLSSolver(m).solve()
show("two-leaf tree, unit weights", r, m.names)
assert r.x[1] + r.x[2] == r.x[0], "leaves must sum to root"

# 2. Shared-leaf DAG with a redundant constraint row.
m = CountDAG()
for name, y in [("root", 20), ("p1", 9), ("p2", 12),
                ("a", 4), ("b", 6), ("c", 5), ("d", 8)]:
    m.add_node(name, observed=y, weight=1)
m.add_parent_equals_children("root", ["p1", "p2"])
m.add_parent_equals_children("p1", ["a", "b"])
m.add_parent_equals_children("p2", ["c", "d"])
m.add_parent_equals_children("root", ["a", "b", "c", "d"])  # redundant
r = WLSSolver(m).solve()
show("shared-leaf DAG, redundant constraint matrix", r, m.names)

# 3. Non-negativity: one observation is negative, bound becomes active.
m = CountDAG()
m.add_node("root", observed=1, weight=1)
m.add_node("a", observed=5, weight=1)
m.add_node("b", observed=-3, weight=1)
m.add_parent_equals_children("root", ["a", "b"])
r = WLSSolver(m, nonneg=True).solve()
show("non-negativity, active bound", r, m.names)
enum = enumerate_active_sets(m, nonneg=True)
print(f"  enumeration agrees: best objective {enum[0]}, active sets {enum[2]}\n")

# 4. Infeasible fixed counts -> Farkas certificate.
m = CountDAG()
m.add_node("root", observed=10, weight=1)
m.add_node("a", observed=3, weight=1)
m.add_node("b", observed=5, weight=1)
m.add_parent_equals_children("root", ["a", "b"])
m.fix("root", 10)
m.fix("a", 3)
m.fix("b", 4)  # 3 + 4 != 10
r = WLSSolver(m).solve()
show("infeasible fixed counts", r, m.names)

# 5. Zero-weight singularity: a free variable the constraints cannot fix.
m = CountDAG()
m.add_node("root", observed=10, weight=1)
m.add_node("a", observed=3, weight=1)
m.add_node("ghost", observed=99, weight=0)  # unconstrained and unobserved
m.add_parent_equals_children("root", ["a"])
r = WLSSolver(m).solve()
show("zero-weight singularity", r, m.names)

# 6. Local observation revision with warm start (certificate re-verified).
m = CountDAG()
m.add_node("root", observed=1, weight=1)
m.add_node("a", observed=5, weight=1)
m.add_node("b", observed=-3, weight=1)
m.add_parent_equals_children("root", ["a", "b"])
solver = WLSSolver(m, nonneg=True)
solver.solve()
r = solver.revise(observed_updates={"b": 7})
show("warm-started revision (b: -3 -> 7)", r, m.names)

# 7. Iteration cap: MAX_ITER is reported, never "optimal".
m = CountDAG()
m.add_node("root", observed=1, weight=1)
m.add_node("a", observed=5, weight=1)
m.add_node("b", observed=-3, weight=1)
m.add_parent_equals_children("root", ["a", "b"])
r = WLSSolver(m, nonneg=True, max_iter=0).solve()
show("iteration cap hit (max_iter=0)", r, m.names)
