"""Run sample for the incremental AC-3 solver.

Run with:  python3.11 demo.py
"""

from ac3 import CSP
from reference import check_solution, enumerate_solutions, reference_ac


def show(csp, label):
    doms = ", ".join(f"{v}={sorted(csp.domains[v])}" for v in sorted(csp.domains))
    print(f"  {label}: {doms}")


def main():
    print("== 1. x<y with domains {1,2} shrinks to x={1}, y={2} ==")
    csp = CSP()
    csp.add_variable("x", {1, 2})
    csp.add_variable("y", {1, 2})
    csp.add_constraint("x", "y", lambda a, b: a < b, cid="lt", description="x<y")
    show(csp, "after propagate")
    for d in csp.deletions:
        print(f"  deleted {d.var}={d.value}: {d.reason}")

    print("== 2. Empty domain is an immediate conflict ==")
    csp2 = CSP()
    csp2.add_variable("x", {2})
    csp2.add_variable("y", {1})
    csp2.add_constraint("x", "y", lambda a, b: a < b, cid="lt", description="x<y")
    print(f"  conflict={csp2.conflict}, x domain={sorted(csp2.domains['x'])}")

    print("== 3. push/pop restores domains, queue, cache and reasons ==")
    csp3 = CSP()
    for v in "xyz":
        csp3.add_variable(v, {1, 2, 3})
    csp3.add_constraint("x", "y", lambda a, b: a < b, cid="c1", description="x<y")
    show(csp3, "before push")
    csp3.push()
    csp3.add_constraint("y", "z", lambda a, b: a < b, cid="c2", description="y<z")
    csp3.assign_value("z", 3)
    show(csp3, "inside branch")
    csp3.pop()
    show(csp3, "after pop")
    print(f"  constraints after pop: {sorted(csp3.constraints)}")

    print("== 4. Removing a constraint restores values (not just deletes) ==")
    csp4 = CSP()
    csp4.add_variable("x", {1, 2})
    csp4.add_variable("y", {1, 2})
    csp4.add_constraint("x", "y", lambda a, b: a < b, cid="lt", description="x<y")
    show(csp4, "with x<y")
    csp4.remove_constraint("lt")
    show(csp4, "after removing x<y")

    print("== 5. Solve 4-queens and verify with the independent checker ==")
    csp5 = CSP()
    n = 4
    for i in range(n):
        csp5.add_variable(f"q{i}", set(range(n)))
    constraints = []
    for i in range(n):
        for j in range(i + 1, n):
            pred = lambda a, b, d=j - i: a != b and abs(a - b) != d
            csp5.add_constraint(f"q{i}", f"q{j}", pred, description=f"q{i}#q{j}")
            constraints.append((f"q{i}", f"q{j}", pred))
    sol = csp5.solve()
    print(f"  solution: {sol}")
    print(f"  independent checker: {check_solution(sol, constraints)}")
    total = enumerate_solutions({f"q{i}": set(range(n)) for i in range(n)}, constraints)
    print(f"  full enumeration found {len(total)} solutions; solver's is among them: {sol in total}")

    print("== 6. Cross-check this session against the independent full-AC reference ==")
    ref = reference_ac({"x": {1, 2}, "y": {1, 2}}, [("x", "y", lambda a, b: a < b)])
    print(f"  reference AC for x<y on {{1,2}}: {ref}")


if __name__ == "__main__":
    main()
