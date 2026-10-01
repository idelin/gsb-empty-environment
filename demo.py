"""Sample runs for the cardinality-constraint CNF encodings.

Usage:  python3.11 demo.py
"""

from cardenc import (ATMOST, ATLEAST, EXACTLY, IncrementalAtMost, VarPool,
                     analyze_propagation, check_direct, encode, solve,
                     verify_projection_equivalence)
from cardenc.verify import count_models_per_projection

OPS = (ATMOST, ATLEAST, EXACTLY)
METHODS = ("seq", "net")


def show_sizes():
    print("== Encoding sizes (n=8, k=3) ==")
    for op in OPS:
        for method in METHODS:
            pool = VarPool()
            x = pool.reserve(8, "x")
            enc = encode(pool, x, op, 3, method=method)
            print(f"  {op:8s} {method:4s} status={enc.status:8s} "
                  f"clauses={enc.num_clauses:4d} aux={enc.num_aux:3d}")


def show_equivalence():
    print("== Projection equivalence (exhaustive, n<=5, k in -1..n+1) ==")
    total = bad = checks = 0
    for n in range(0, 6):
        for k in range(-1, n + 2):
            for op in OPS:
                for method in METHODS:
                    rep = verify_projection_equivalence(VarPool(), n, op, k, method)
                    total += 1
                    checks += rep["checked"]
                    bad += 0 if rep["ok"] else 1
    print(f"  {total} encodings verified over {checks} original assignments, "
          f"mismatches: {bad}")


def show_model_enumeration():
    print("== Exhaustive auxiliary enumeration (n=4, at-most 2) ==")
    for method in METHODS:
        pool = VarPool()
        x = pool.reserve(4, "x")
        enc = encode(pool, x, ATMOST, 2, method=method)
        counts = count_models_per_projection(enc)
        sat = sum(1 for c in counts.values() if c > 0)
        print(f"  {method}: aux={enc.num_aux}, {len(counts)} original assignments, "
              f"{sat} with >=1 extension, total CNF models {sum(counts.values())}")


def show_propagation():
    print("== Propagation strength (n=6, k=2) ==")
    for op in (ATMOST, EXACTLY):
        for method in METHODS:
            rep = analyze_propagation(VarPool(), 6, op, 2, method)
            print(f"  {op:8s} {method:4s} clauses={rep['clauses']:4d} "
                  f"aux={rep['aux']:3d} GAC={rep['gac']} "
                  f"conflict_on_violation={rep['conflict_detected']} "
                  f"avg_propagated={rep['avg_propagated']:.1f} "
                  f"trials={rep['trials']}")


def show_incremental():
    print("== Incremental tightening / retraction via assumptions ==")
    pool = VarPool()
    x = pool.reserve(5, "x")
    inc = IncrementalAtMost(pool, x, capacity=3)
    a2 = inc.tighten(2)
    m = solve(inc.cnf, assumptions=[a2])
    vals = [m[v] for v in x]
    print(f"  bound<=2 : model={vals} independent_check={check_direct(ATMOST, 2, vals)}")
    a1 = inc.tighten(1)  # tighten: only adds clauses; a2 no longer assumed
    m1 = solve(inc.cnf, assumptions=[a1])
    vals1 = [m1[v] for v in x]
    print(f"  bound<=1 : model={vals1} independent_check={check_direct(ATMOST, 1, vals1)}")
    forced = solve(inc.cnf, assumptions=[a1, x[0], x[1]])
    print(f"  bound<=1 with x1,x2 forced: {'UNSAT (as expected)' if forced is None else 'SAT'}")
    m2 = solve(inc.cnf, assumptions=[a2, x[0], x[1]])  # retract to weaker bound
    vals2 = [m2[v] for v in x]
    print(f"  retract to <=2 with x1,x2: model={vals2} "
          f"independent_check={check_direct(ATMOST, 2, vals2)}")


def show_boundaries():
    print("== Boundary semantics ==")
    cases = [(ATMOST, -1, 3), (ATMOST, 5, 3), (ATMOST, 0, 3),
             (ATLEAST, -1, 3), (ATLEAST, 5, 3), (ATLEAST, 3, 3),
             (EXACTLY, 0, 0), (EXACTLY, 2, 0)]
    for op, k, n in cases:
        pool = VarPool()
        x = pool.reserve(n, "x")
        enc = encode(pool, x, op, k, method="seq")
        print(f"  {op:8s} k={k:2d} n={n}: status={enc.status:14s} "
              f"clauses={enc.num_clauses}")


if __name__ == "__main__":
    show_sizes()
    show_equivalence()
    show_model_enumeration()
    show_propagation()
    show_incremental()
    show_boundaries()
