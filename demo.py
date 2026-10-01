"""Runnable samples for the symbreak package (Python 3.11, stdlib only).

Run:  python3.11 demo.py
"""

from symbreak import (
    BudgetExceeded,
    Constraint,
    Problem,
    SymmetryRejected,
    add_symmetry_generators,
    check_original,
    declare_exchangeable,
    optimize,
    recover_symmetric,
    solve_all,
    verify_automorphism,
    verify_small,
)
from symbreak.permutations import normalize


def banner(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def sample_exchangeable_group():
    banner("Sample 1: fully exchangeable group -> ordering constraints + recovery")
    problem = Problem(
        domains={v: (0, 1, 2) for v in "abcd"},
        constraints=[
            Constraint("sum==3", tuple("abcd"), lambda a, b, c, d: a + b + c + d == 3),
        ],
    )
    added, group = declare_exchangeable(problem, list("abcd"))
    print("declared exchangeable: a b c d (domains all {0,1,2})")
    print("added constraints    :", [c.name for c in added])
    print("group size (S_4)     :", len(group))

    report = verify_small(problem, group)
    print(report.summary())
    assert report.ok

    retained = solve_all(problem)
    recovered = recover_symmetric(retained, group)
    print("recovered solutions  :", len(recovered), "(== original count:",
          report.total_solutions, ")")
    print("every retained solution passes the independent checker:",
          all(check_original(problem, s) for s in retained))


def sample_wrong_permutation():
    banner("Sample 2: wrong permutation -> minimal counterexample")
    problem = Problem(
        domains={"x": (0, 1, 2), "y": (0, 1, 2)},
        constraints=[Constraint("x<=y", ("x", "y"), lambda x, y: x <= y)],
    )
    swap = normalize({"x": "y", "y": "x"}, problem.variables)
    cx = verify_automorphism(problem, swap)
    print("permutation: swap x<->y;  constraint: x<=y")
    print("counterexample:", cx.describe())
    print("minimal violating assignment:", dict(cx.assignment))


def sample_generators_and_objective():
    banner("Sample 3: rotation generator, budget, objective preservation")
    variables = ["x1", "x2", "x3"]
    problem = Problem(
        domains={v: (0, 1, 2) for v in variables},
        constraints=[
            Constraint("sum==2", tuple(variables), lambda a, b, c: a + b + c == 2),
            Constraint(
                "pair-products==1",
                tuple(variables),
                lambda a, b, c: a * b + b * c + c * a == 1,
            ),
        ],
        objective=lambda a: a["x1"] + a["x2"] + a["x3"],
        objective_name="sum",
    )
    rotation = {"x1": "x2", "x2": "x3", "x3": "x1"}

    bad_objective = Problem(
        domains=problem.domains,
        constraints=problem.constraints,
        objective=lambda a: a["x1"],  # not rotation-invariant
        objective_name="x1-only",
    )
    cx = verify_automorphism(bad_objective, normalize(rotation, variables))
    print("objective 'x1-only' rejected:", cx.describe())

    added, group = add_symmetry_generators(problem, [rotation], budget=10)
    print("rotation accepted; closure size:", len(group))
    print("added lex constraints:", [c.name for c in added])

    report = verify_small(problem, group)
    print(report.summary())
    assert report.ok

    best, val = optimize(problem, maximize=True)
    print("optimal representative:", best, "objective:", val,
          "| passes independent checker:", check_original(problem, best))

    try:
        add_symmetry_generators(problem, [rotation], budget=2)
    except BudgetExceeded as exc:
        print("budget respected:", exc)


def sample_rejections():
    banner("Sample 4: declarations that must be rejected")

    dom_problem = Problem(
        domains={"p": (0, 1), "q": (0, 1, 2)},
        constraints=[],
    )
    try:
        declare_exchangeable(dom_problem, ["p", "q"])
    except SymmetryRejected as exc:
        print("domain mismatch rejected:", exc.counterexample.describe())

    special_problem = Problem(
        domains={"captain": (0, 1), "mate": (0, 1)},
        constraints=[],
        special=frozenset({"captain"}),
    )
    try:
        declare_exchangeable(special_problem, ["captain", "mate"])
    except SymmetryRejected as exc:
        print("special role rejected  :", exc.counterexample.describe())

    asym_problem = Problem(
        domains={"u": (0, 1), "v": (0, 1)},
        constraints=[Constraint("u<=v", ("u", "v"), lambda u, v: u <= v)],
    )
    try:
        declare_exchangeable(asym_problem, ["u", "v"])
    except SymmetryRejected as exc:
        print("constraint mismatch    :", exc.counterexample.describe())
        print("  minimal counterexample:", dict(exc.counterexample.assignment))


def sample_small_domain_exhaustive():
    banner("Sample 5: tiny domains, exhaustive orbit check (no class lost)")
    problem = Problem(
        domains={v: (0, 1) for v in "xyz"},
        constraints=[
            Constraint("at-least-one", tuple("xyz"), lambda x, y, z: x + y + z >= 1),
        ],
    )
    added, group = declare_exchangeable(problem, list("xyz"))
    report = verify_small(problem, group)
    print("added constraints:", [c.name for c in added])
    print(report.summary())
    assert report.ok
    assert report.retained_solutions == report.num_orbits  # exactly one per class


if __name__ == "__main__":
    sample_exchangeable_group()
    sample_wrong_permutation()
    sample_generators_and_objective()
    sample_rejections()
    sample_small_domain_exhaustive()
    print("\nAll samples completed; every verification reported OK.")
