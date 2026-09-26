"""Runnable examples for csp_enum (Python 3.11+, standard library only).

Run:  python3 examples.py
"""

from csp_enum import (BUDGET, EXHAUSTED, Problem, SolutionEnumerator,
                      all_different, constraint)


def banner(title):
    print("\n" + "=" * 60)
    print(title)
    print("=" * 60)


def example_map_coloring():
    banner("1) Map coloring, enumerate all, projection dedup")
    # Triangle a-b-c plus a tail c-d, 3 colors.  Full colorings: 12.
    # Projection onto {a, b}: only 6 distinct outputs.
    p = Problem()
    for v in "abcd":
        p.add_variable(v, ("R", "G", "B"))
    for u, v in [("a", "b"), ("b", "c"), ("c", "a"), ("c", "d")]:
        p.add_constraint(constraint(
            (u, v), lambda vals, u=u, v=v: vals[u] != vals[v],
            name=f"{u}!={v}"))

    full = SolutionEnumerator(p)  # projection defaults to all variables
    print(f"full assignments      : {len(full.enumerate_all())}")

    proj = SolutionEnumerator(p, projection=("a", "b"))
    sols = proj.enumerate_all()
    print(f"projection (a, b)     : {len(sols)} distinct outputs")
    for s in sols:
        print(f"  a={s['a']} b={s['b']}")


def example_auxiliary_dedup():
    banner("2) Auxiliary variables: same projection -> single output")
    # x is the output bit, t is an auxiliary witness with 3 values.
    # 6 full assignments exist, but only 2 distinct projections onto x.
    p = Problem()
    p.add_variable("x", (0, 1))
    p.add_variable("t", (0, 1, 2))
    p.add_constraint(constraint(("x", "t"),
                                lambda vals: vals["t"] >= vals["x"],
                                name="t>=x"))
    enum = SolutionEnumerator(p, projection=("x",))
    sols = enum.enumerate_all()
    print(f"projection (x,)       : {sols}  (6 full assignments collapsed)")


def example_empty_projection():
    banner("3) Empty projection: at most one solution (satisfiability)")
    p = Problem()
    p.add_variable("x", (0, 1))
    p.add_variable("y", (0, 1))
    p.add_constraint(constraint(("x", "y"),
                                lambda vals: vals["x"] + vals["y"] == 1,
                                name="x+y==1"))
    sat = SolutionEnumerator(p, projection=())
    print(f"satisfiable problem   : {sat.enumerate_all()}  (one empty dict)")

    q = Problem()
    q.add_variable("x", (0, 1))
    q.add_constraint(constraint(("x",), lambda vals: vals["x"] == 0, "x==0"))
    q.add_constraint(constraint(("x",), lambda vals: vals["x"] == 1, "x==1"))
    unsat = SolutionEnumerator(q, projection=())
    print(f"unsatisfiable problem : {unsat.enumerate_all()}  (no output)")


def example_pause_resume():
    banner("4) Pause / resume with a step budget (no duplicate output)")
    # x+y+z == 3 over {0,1,2}: 7 solutions.
    def build():
        p = Problem()
        for v in "xyz":
            p.add_variable(v, (0, 1, 2))
        p.add_constraint(constraint(
            ("x", "y", "z"),
            lambda vals: vals["x"] + vals["y"] + vals["z"] == 3,
            name="x+y+z==3"))
        return p

    reference = SolutionEnumerator(build()).enumerate_all()
    print(f"uninterrupted run     : {len(reference)} solutions")

    enum = SolutionEnumerator(build())
    collected, calls = [], 0
    while True:
        res = enum.search(budget=3)  # tiny budget: pause every 3 steps
        calls += 1
        collected.extend(res.solutions)
        print(f"  call {calls}: +{len(res.solutions)} sol, "
              f"{res.steps} steps, status={res.status}")
        if res.status != BUDGET:
            break
    assert res.status == EXHAUSTED
    key = lambda s: tuple(sorted(s.items()))
    assert sorted(map(key, collected)) == sorted(map(key, reference))
    assert len(collected) == len(set(map(key, collected)))
    print(f"resumed run           : {len(collected)} solutions, "
          f"identical to uninterrupted, no duplicates")

    # Serialisable snapshot: export mid-search, restore, finish.
    enum2 = SolutionEnumerator(build())
    enum2.search(budget=4)
    state = enum2.export_state()
    restored = SolutionEnumerator.restore(build(), state)
    rest = restored.enumerate_all()
    got = sorted(tuple(sorted(s.items())) for s in
                 [dict(zip("xyz", k)) for k in enum2.emitted] + rest)
    assert got == sorted(map(key, reference))
    print(f"export/restore        : resumed from snapshot, same {len(got)} "
          f"solutions")


def example_first_n_and_queens():
    banner("5) First N solutions: 4-queens")
    p = Problem()
    for r in range(4):
        p.add_variable(f"r{r}", range(4))
    p.add_constraint(all_different("r0", "r1", "r2", "r3"))
    for i in range(4):
        for j in range(i + 1, 4):
            p.add_constraint(constraint(
                (f"r{i}", f"r{j}"),
                lambda vals, i=i, j=j:
                    abs(vals[f"r{i}"] - vals[f"r{j}"]) != j - i,
                name=f"diag{i}{j}"))
    enum = SolutionEnumerator(p)
    first1 = enum.first(1)
    print(f"first(1)              : {first1}")
    rest = enum.enumerate_all()
    print(f"remaining             : {rest}")
    print(f"total 4-queens sols   : {1 + len(rest)} (expected 2)")


if __name__ == "__main__":
    example_map_coloring()
    example_auxiliary_dedup()
    example_empty_projection()
    example_pause_resume()
    example_first_n_and_queens()
    print("\nAll examples finished.")
