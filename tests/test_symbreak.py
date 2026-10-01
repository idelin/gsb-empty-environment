"""unittest suite for the symbreak package (stdlib only)."""

import itertools
import unittest

from symbreak import (
    BudgetExceeded,
    Constraint,
    Problem,
    SymmetryRejected,
    add_symmetry_generators,
    apply_perm,
    check_original,
    closure,
    declare_exchangeable,
    normalize,
    optimize,
    recover_symmetric,
    solve_all,
    verify_automorphism,
    verify_small,
)
from symbreak.recovery import assignment_key


def sum_problem(n=4, domain=(0, 1, 2), total=3):
    variables = [f"v{i}" for i in range(n)]
    return Problem(
        domains={v: domain for v in variables},
        constraints=[
            Constraint(
                f"sum=={total}",
                tuple(variables),
                lambda *vals, total=total: sum(vals) == total,
            )
        ],
    )


class TestPermutationVerification(unittest.TestCase):
    def test_valid_swap_accepted(self):
        problem = sum_problem()
        swap = normalize({"v0": "v1", "v1": "v0"}, problem.variables)
        self.assertIsNone(verify_automorphism(problem, swap))

    def test_non_bijection_rejected(self):
        problem = sum_problem(n=2)
        with self.assertRaises(ValueError):
            normalize({"v0": "v1", "v1": "v1"}, problem.variables)

    def test_wrong_permutation_minimal_counterexample(self):
        problem = Problem(
            domains={"x": (0, 1, 2), "y": (0, 1, 2)},
            constraints=[Constraint("x<=y", ("x", "y"), lambda x, y: x <= y)],
        )
        swap = normalize({"x": "y", "y": "x"}, problem.variables)
        cx = verify_automorphism(problem, swap)
        self.assertIsNotNone(cx)
        self.assertEqual(cx.kind, "constraint")
        self.assertEqual(cx.constraint, "x<=y")
        self.assertEqual(dict(cx.assignment), {"x": 0, "y": 1})
        # Minimality: no lexicographically smaller assignment violates.
        for asn in problem.assignments():
            key = tuple(sorted(asn.items()))
            if key >= cx.assignment:
                break
            self.assertEqual(
                problem.constraints[0].holds(asn),
                problem.constraints[0].holds(apply_perm(swap, asn)),
            )

    def test_special_role_cannot_be_moved(self):
        problem = Problem(
            domains={"root": (0, 1), "leaf": (0, 1)},
            constraints=[],
            special=frozenset({"root"}),
        )
        swap = normalize({"root": "leaf", "leaf": "root"}, problem.variables)
        cx = verify_automorphism(problem, swap)
        self.assertIsNotNone(cx)
        self.assertEqual(cx.kind, "special_moved")
        self.assertEqual(cx.variable, "root")

    def test_domain_mismatch_rejected(self):
        problem = Problem(domains={"a": (0, 1), "b": (0, 1, 2)}, constraints=[])
        swap = normalize({"a": "b", "b": "a"}, problem.variables)
        cx = verify_automorphism(problem, swap)
        self.assertIsNotNone(cx)
        self.assertEqual(cx.kind, "domain_mismatch")

    def test_objective_must_be_preserved(self):
        problem = Problem(
            domains={"x": (0, 1), "y": (0, 1)},
            constraints=[],
            objective=lambda a: a["x"],
            objective_name="x-only",
        )
        swap = normalize({"x": "y", "y": "x"}, problem.variables)
        cx = verify_automorphism(problem, swap)
        self.assertIsNotNone(cx)
        self.assertEqual(cx.kind, "objective")
        self.assertEqual(dict(cx.assignment), {"x": 0, "y": 1})


class TestGroupClosure(unittest.TestCase):
    def test_cyclic_closure(self):
        variables = ["a", "b", "c"]
        rot = normalize({"a": "b", "b": "c", "c": "a"}, variables)
        group = closure([rot], variables, budget=10)
        self.assertEqual(len(group), 3)

    def test_full_symmetric_closure(self):
        variables = ["a", "b", "c"]
        s1 = normalize({"a": "b", "b": "a"}, variables)
        s2 = normalize({"b": "c", "c": "b"}, variables)
        group = closure([s1, s2], variables, budget=10)
        self.assertEqual(len(group), 6)

    def test_budget_exceeded(self):
        variables = ["a", "b", "c"]
        s1 = normalize({"a": "b", "b": "a"}, variables)
        s2 = normalize({"b": "c", "c": "b"}, variables)
        with self.assertRaises(BudgetExceeded):
            closure([s1, s2], variables, budget=5)


class TestLexConstraints(unittest.TestCase):
    def test_exchangeable_adds_ordering_only_on_group(self):
        problem = sum_problem()
        added, group = declare_exchangeable(problem, ["v0", "v1", "v2"])
        self.assertEqual(len(added), 2)
        self.assertEqual(len(group), 6)
        touched = {v for c in problem.symmetry_constraints for v in c.scope}
        self.assertEqual(touched, {"v0", "v1", "v2"})
        # v3 was never declared symmetric: it must not be ordered.
        self.assertNotIn("v3", touched)

    def test_reject_group_with_asymmetric_constraint(self):
        problem = Problem(
            domains={"u": (0, 1), "v": (0, 1)},
            constraints=[Constraint("u<=v", ("u", "v"), lambda u, v: u <= v)],
        )
        with self.assertRaises(SymmetryRejected) as ctx:
            declare_exchangeable(problem, ["u", "v"])
        self.assertEqual(ctx.exception.counterexample.kind, "constraint")
        self.assertEqual(problem.symmetry_constraints, [])  # nothing added

    def test_reject_group_with_mismatched_domains(self):
        problem = Problem(domains={"u": (0, 1), "v": (0, 1, 2)}, constraints=[])
        with self.assertRaises(SymmetryRejected):
            declare_exchangeable(problem, ["u", "v"])

    def test_generators_verified_before_any_constraint_added(self):
        problem = Problem(
            domains={"x": (0, 1), "y": (0, 1)},
            constraints=[Constraint("x<=y", ("x", "y"), lambda x, y: x <= y)],
        )
        with self.assertRaises(SymmetryRejected):
            add_symmetry_generators(problem, [{"x": "y", "y": "x"}])
        self.assertEqual(problem.symmetry_constraints, [])

    def test_lex_constraints_added_per_nonidentity_element(self):
        variables = ["a", "b", "c"]
        problem = Problem(
            domains={v: (0, 1, 2) for v in variables},
            constraints=[
                Constraint("sum==2", tuple(variables), lambda a, b, c: a + b + c == 2)
            ],
        )
        rot = {"a": "b", "b": "c", "c": "a"}
        added, group = add_symmetry_generators(problem, [rot], budget=10)
        self.assertEqual(len(group), 3)
        self.assertEqual(len(added), 2)  # one per non-identity element


class TestSolverAndIndependentChecker(unittest.TestCase):
    def test_solver_matches_naive_enumeration(self):
        problem = sum_problem(n=3, domain=(0, 1, 2), total=3)
        got = {assignment_key(s) for s in solve_all(problem, include_symmetry=False)}
        want = set()
        for vals in itertools.product((0, 1, 2), repeat=3):
            if sum(vals) == 3:
                want.add(tuple(sorted(zip(["v0", "v1", "v2"], vals))))
        self.assertEqual(got, want)

    def test_claimed_solutions_pass_independent_checker(self):
        problem = sum_problem()
        declare_exchangeable(problem, problem.variables)
        for sol in solve_all(problem):
            self.assertTrue(check_original(problem, sol))

    def test_symmetry_solutions_are_subset_of_original(self):
        problem = sum_problem()
        declare_exchangeable(problem, problem.variables)
        full = {assignment_key(s) for s in solve_all(problem, include_symmetry=False)}
        kept = {assignment_key(s) for s in solve_all(problem)}
        self.assertTrue(kept <= full)

    def test_optimize_respects_symmetric_objective(self):
        problem = sum_problem(n=3, domain=(0, 1, 2), total=3)
        problem.objective = lambda a: sum(a.values())
        declare_exchangeable(problem, problem.variables)
        best, val = optimize(problem, maximize=True)
        self.assertEqual(val, 3)
        self.assertTrue(check_original(problem, best))


class TestOrbitsAndRecovery(unittest.TestCase):
    def test_every_orbit_keeps_a_solution_and_deleted_recoverable(self):
        problem = sum_problem()
        _, group = declare_exchangeable(problem, problem.variables)
        report = verify_small(problem, group)
        self.assertTrue(report.ok, report.summary())
        self.assertEqual(report.orbits_covered, report.num_orbits)
        self.assertGreater(report.total_solutions, report.retained_solutions)

    def test_recovery_reconstructs_full_solution_set(self):
        problem = sum_problem(n=3, domain=(0, 1), total=2)
        _, group = declare_exchangeable(problem, problem.variables)
        full = {assignment_key(s) for s in solve_all(problem, include_symmetry=False)}
        retained = solve_all(problem)
        recovered = {assignment_key(s) for s in recover_symmetric(retained, group)}
        self.assertEqual(recovered, full)

    def test_rotation_group_orbits(self):
        variables = ["a", "b", "c"]
        problem = Problem(
            domains={v: (0, 1, 2) for v in variables},
            constraints=[
                Constraint("sum==2", tuple(variables), lambda a, b, c: a + b + c == 2),
                Constraint(
                    "pp==1",
                    tuple(variables),
                    lambda a, b, c: a * b + b * c + c * a == 1,
                ),
            ],
        )
        _, group = add_symmetry_generators(
            problem, [{"a": "b", "b": "c", "c": "a"}], budget=10
        )
        report = verify_small(problem, group)
        self.assertTrue(report.ok, report.summary())
        self.assertEqual(report.total_solutions, 3)
        self.assertEqual(report.retained_solutions, 1)

    def test_tiny_domain_exhaustive_no_lost_class(self):
        problem = Problem(
            domains={v: (0, 1) for v in "xyz"},
            constraints=[
                Constraint("ge1", tuple("xyz"), lambda x, y, z: x + y + z >= 1)
            ],
        )
        _, group = declare_exchangeable(problem, list("xyz"))
        report = verify_small(problem, group)
        self.assertTrue(report.ok, report.summary())
        self.assertEqual(report.retained_solutions, report.num_orbits)

    def test_untouched_variables_keep_all_values(self):
        # Declared symmetry on {v0, v1} must not restrict v2 at all.
        problem = sum_problem(n=3, domain=(0, 1), total=1)
        declare_exchangeable(problem, ["v0", "v1"])
        retained = solve_all(problem)
        self.assertTrue(retained)
        for sol in retained:
            self.assertEqual(sol["v0"] <= sol["v1"], True)


if __name__ == "__main__":
    unittest.main()
