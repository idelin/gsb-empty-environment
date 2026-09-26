"""Unit tests for csp_enum (Python 3.11+, standard library only).

Run:  python3 -m unittest -v
"""

import itertools
import unittest

from csp_enum import (BUDGET, EXHAUSTED, LIMIT, Constraint, Problem,
                      SolutionEnumerator, SolverIntegrityError,
                      all_different, constraint, table, verify_solution)


def brute_force_projections(problem, projection):
    """Reference: enumerate distinct projections by exhaustive product."""
    names = list(problem.domains)
    out, seen = [], set()
    for values in itertools.product(*(problem.domains[n] for n in names)):
        assignment = dict(zip(names, values))
        if verify_solution(problem, assignment):
            key = tuple(assignment[v] for v in projection)
            if key not in seen:
                seen.add(key)
                out.append(key)
    return out


def make_sum_problem(total=3, domain=(0, 1, 2)):
    p = Problem()
    for v in "xyz":
        p.add_variable(v, domain)
    p.add_constraint(constraint(
        ("x", "y", "z"),
        lambda vals: vals["x"] + vals["y"] + vals["z"] == total,
        name="x+y+z==total"))
    return p


def key_of(proj):
    return tuple(sorted(proj.items()))


class TestIndependentChecker(unittest.TestCase):
    def test_accepts_valid_solution(self):
        p = make_sum_problem()
        self.assertTrue(verify_solution(p, {"x": 1, "y": 1, "z": 1}))

    def test_rejects_constraint_violation(self):
        p = make_sum_problem()
        self.assertFalse(verify_solution(p, {"x": 0, "y": 0, "z": 0}))

    def test_rejects_value_outside_domain(self):
        p = make_sum_problem()
        self.assertFalse(verify_solution(p, {"x": 9, "y": 0, "z": 0}))

    def test_rejects_incomplete_and_extra_variables(self):
        p = make_sum_problem()
        self.assertFalse(verify_solution(p, {"x": 1, "y": 1}))
        self.assertFalse(verify_solution(p, {"x": 1, "y": 1, "z": 1, "w": 0}))

    def test_checker_is_independent_of_solver_state(self):
        # The checker only knows the original problem; blocking clauses
        # added by the enumerator must not affect it.
        p = make_sum_problem()
        enum = SolutionEnumerator(p)
        first = enum.first(1)[0]
        self.assertTrue(verify_solution(p, first))


class TestEnumerateAll(unittest.TestCase):
    def test_matches_brute_force(self):
        p = make_sum_problem()
        enum = SolutionEnumerator(p)
        got = sorted(key_of(s) for s in enum.enumerate_all())
        names = tuple(p.domains)
        want = sorted(tuple(sorted(zip(names, k))) for k in
                      brute_force_projections(p, names))
        self.assertEqual(got, want)
        self.assertEqual(len(got), 7)
        self.assertEqual(enum.done, True)

    def test_every_output_passes_independent_checker(self):
        p = make_sum_problem()
        for sol in SolutionEnumerator(p).enumerate_all():
            self.assertTrue(verify_solution(p, sol))

    def test_first_n_then_resume(self):
        p = make_sum_problem()
        enum = SolutionEnumerator(p)
        first2 = enum.first(2)
        self.assertEqual(len(first2), 2)
        rest = enum.enumerate_all()
        all_sols = [key_of(s) for s in first2 + rest]
        self.assertEqual(len(all_sols), 7)
        self.assertEqual(len(set(all_sols)), 7)  # no duplicates across calls

    def test_limit_status_and_resume(self):
        p = make_sum_problem()
        enum = SolutionEnumerator(p)
        res = enum.search(max_solutions=3)
        self.assertEqual(res.status, LIMIT)
        self.assertEqual(len(res.solutions), 3)
        res2 = enum.search()
        self.assertEqual(res2.status, EXHAUSTED)
        self.assertEqual(len(res.solutions) + len(res2.solutions), 7)

    def test_unsatisfiable_problem(self):
        p = Problem()
        p.add_variable("x", (0, 1))
        p.add_constraint(constraint(("x",), lambda v: v["x"] == 0))
        p.add_constraint(constraint(("x",), lambda v: v["x"] == 1))
        enum = SolutionEnumerator(p)
        res = enum.search()
        self.assertEqual(res.solutions, [])
        self.assertEqual(res.status, EXHAUSTED)


class TestProjectionDedup(unittest.TestCase):
    def test_auxiliary_variables_collapse(self):
        # 2 * 3 = 6 full assignments, only 2 distinct projections on x.
        p = Problem()
        p.add_variable("x", (0, 1))
        p.add_variable("t", (0, 1, 2))
        p.add_constraint(constraint(("x", "t"),
                                    lambda v: v["t"] >= v["x"]))
        sols = SolutionEnumerator(p, projection=("x",)).enumerate_all()
        self.assertEqual(sorted(key_of(s) for s in sols),
                         [(("x", 0),), (("x", 1),)])

    def test_projection_matches_brute_force(self):
        p = make_sum_problem()
        for projection in [("x",), ("x", "y"), ("z", "y"), ("x", "y", "z")]:
            got = sorted(tuple(s[v] for v in projection)
                         for s in SolutionEnumerator(p, projection)
                         .enumerate_all())
            want = sorted(brute_force_projections(p, projection))
            self.assertEqual(got, want, f"projection={projection}")

    def test_empty_projection_at_most_one(self):
        p = make_sum_problem()
        sols = SolutionEnumerator(p, projection=()).enumerate_all()
        self.assertEqual(sols, [{}])  # satisfiable: exactly one empty dict

        q = Problem()
        q.add_variable("x", (0,))
        q.add_constraint(constraint(("x",), lambda v: v["x"] == 1))
        sols2 = SolutionEnumerator(q, projection=()).enumerate_all()
        self.assertEqual(sols2, [])  # unsatisfiable: nothing

    def test_blocking_excludes_only_emitted(self):
        # After emitting everything, the set of emitted projections must
        # equal the brute-force set: blocking removed nothing more.
        p = make_sum_problem()
        projection = ("x", "z")
        enum = SolutionEnumerator(p, projection)
        got = sorted(tuple(s[v] for v in projection)
                     for s in enum.enumerate_all())
        self.assertEqual(got, sorted(brute_force_projections(p, projection)))


class TestPauseResume(unittest.TestCase):
    def test_budget_slices_equal_uninterrupted(self):
        projection = ("x", "y", "z")
        want = sorted(tuple(sorted(zip(projection, k))) for k in
                      brute_force_projections(make_sum_problem(), projection))
        for budget in (1, 2, 3, 5, 17):
            enum = SolutionEnumerator(make_sum_problem())
            collected = []
            statuses = []
            while True:
                res = enum.search(budget=budget)
                collected.extend(res.solutions)
                statuses.append(res.status)
                if res.status != BUDGET:
                    break
            self.assertEqual(statuses[-1], EXHAUSTED)
            self.assertTrue(all(s == BUDGET for s in statuses[:-1]))
            got = sorted(key_of(s) for s in collected)
            self.assertEqual(got, want, f"budget={budget}")
            self.assertEqual(len(got), len(set(got)), "duplicate output")

    def test_budget_zero_suspends_immediately(self):
        enum = SolutionEnumerator(make_sum_problem())
        res = enum.search(budget=0)
        self.assertEqual(res.status, BUDGET)
        self.assertEqual(res.solutions, [])
        self.assertFalse(enum.done)
        self.assertEqual(len(enum.enumerate_all()), 7)

    def test_search_stack_preserved_on_budget(self):
        enum = SolutionEnumerator(make_sum_problem())
        enum.search(budget=2)
        self.assertGreater(len(enum._stack), 0)  # resumable stack retained
        self.assertFalse(enum.done)

    def test_export_restore_roundtrip(self):
        projection = ("x", "y", "z")
        want = sorted(tuple(sorted(zip(projection, k))) for k in
                      brute_force_projections(make_sum_problem(), projection))
        for budget in (1, 2, 4, 9):
            enum = SolutionEnumerator(make_sum_problem())
            enum.search(budget=budget)
            state = enum.export_state()
            restored = SolutionEnumerator.restore(make_sum_problem(), state)
            rest = restored.enumerate_all()
            got = sorted([key_of(dict(zip(projection, k)))
                          for k in enum.emitted] +
                         [key_of(s) for s in rest])
            self.assertEqual(got, want, f"budget={budget}")
            self.assertEqual(len(got), len(set(got)))

    def test_exported_state_is_json_compatible(self):
        import json
        enum = SolutionEnumerator(make_sum_problem())
        enum.search(budget=3)
        state = json.loads(json.dumps(enum.export_state()))
        restored = SolutionEnumerator.restore(make_sum_problem(), state)
        self.assertEqual(len(restored.enumerate_all()) +
                         len(enum.emitted), 7)


class TestConstraints(unittest.TestCase):
    def test_all_different(self):
        p = Problem()
        for v in "abc":
            p.add_variable(v, (1, 2, 3))
        p.add_constraint(all_different("a", "b", "c"))
        sols = SolutionEnumerator(p).enumerate_all()
        self.assertEqual(len(sols), 6)  # 3! permutations

    def test_table_constraint(self):
        p = Problem()
        p.add_variable("x", (0, 1, 2))
        p.add_variable("y", (0, 1, 2))
        p.add_constraint(table(("x", "y"), [(0, 1), (1, 2), (2, 0)]))
        sols = SolutionEnumerator(p).enumerate_all()
        self.assertEqual(sorted(key_of(s) for s in sols),
                         sorted([(("x", 0), ("y", 1)),
                                 (("x", 1), ("y", 2)),
                                 (("x", 2), ("y", 0))]))

    def test_unknown_variable_rejected(self):
        p = Problem()
        p.add_variable("x", (0, 1))
        with self.assertRaises(ValueError):
            p.add_constraint(constraint(("nope",), lambda v: True))
        with self.assertRaises(ValueError):
            SolutionEnumerator(p, projection=("nope",))
        with self.assertRaises(ValueError):
            p.add_variable("x", (0,))          # duplicate
        with self.assertRaises(ValueError):
            Problem().add_variable("e", ())    # empty domain


class TestIntegrityGuard(unittest.TestCase):
    def test_lying_constraint_triggers_integrity_error(self):
        # A constraint whose solver-path check always says "undecided"
        # simulates a solver bug; the independent checker must catch it.
        class LyingConstraint(Constraint):
            def check_partial(self, assignment):
                return None

        p = Problem()
        p.add_variable("x", (0, 1))
        p.add_constraint(LyingConstraint(
            ("x",), lambda v: v["x"] == 1, name="x==1"))
        enum = SolutionEnumerator(p)
        with self.assertRaises(SolverIntegrityError):
            enum.enumerate_all()

    def test_no_variable_problem(self):
        p = Problem()
        sols = SolutionEnumerator(p, projection=()).enumerate_all()
        self.assertEqual(sols, [{}])


if __name__ == "__main__":
    unittest.main()
