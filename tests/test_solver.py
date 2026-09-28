from __future__ import annotations

import unittest

from fdcsp import Assumption, Solver, validate_assignment


def make_solver() -> Solver:
    solver = Solver({"x": {1, 2, 3}, "y": {1, 2, 3}, "z": {1, 2, 3}})
    solver.add_constraint(["x"], lambda x: x != 1, "x != 1")
    solver.add_constraint(["x", "y"], lambda x, y: x != y, "x != y")
    solver.add_constraint(["y", "z"], lambda y, z: y != z, "y != z")
    solver.add_constraint(["x", "z"], lambda x, z: x != z, "x != z")
    return solver


class SolverTests(unittest.TestCase):
    def test_solution_is_independently_checked(self) -> None:
        solver = make_solver()
        self.assertTrue(solver.propagate())

        with solver.assume("y", 3):
            solution = solver.solve()

        self.assertEqual(solution, {"x": 2, "y": 3, "z": 1})
        self.assertTrue(validate_assignment(
            solution, solver.initial_domains(), solver.constraints()
        ))
        self.assertTrue(validate_assignment(
            solution,
            solver.initial_domains(),
            solver.constraints(),
            [Assumption("y", 3)],
        ))

    def test_search_does_not_mutate_current_scope(self) -> None:
        solver = make_solver()
        with solver.assume("y", 3):
            before = solver.domains()
            self.assertEqual(solver.solve(), {"x": 2, "y": 3, "z": 1})
            self.assertEqual(solver.domains(), before)

    def test_nested_derivations_are_attributed_and_rolled_back(self) -> None:
        solver = make_solver()
        self.assertTrue(solver.propagate())
        base_domains = solver.domains()

        with solver.assuming((("y", 3),)):
            self.assertTrue(solver.propagate())
            self.assertEqual(solver.domains(), {"x": frozenset({2}), "y": frozenset({3}), "z": frozenset({1})})
            outer_removals = {(item.variable, item.value) for item in solver.domains_store.removals_by_level()[1]}
            self.assertEqual(
                outer_removals,
                {("x", 3), ("y", 1), ("y", 2), ("z", 2), ("z", 3)},
            )

            with solver.assume("x", 3):
                self.assertFalse(solver.propagate())
                self.assertEqual(solver.scope_depth, 2)

            self.assertEqual(solver.scope_depth, 1)
            self.assertEqual(solver.domains(), {"x": frozenset({2}), "y": frozenset({3}), "z": frozenset({1})})
            self.assertEqual(solver.solve(), {"x": 2, "y": 3, "z": 1})

        self.assertEqual(solver.domains(), base_domains)

    def test_inner_derivations_remain_distinct_from_outer_level(self) -> None:
        solver = make_solver()
        self.assertTrue(solver.propagate())

        with solver.assume("x", 2):
            self.assertTrue(solver.propagate())
            self.assertEqual(
                solver.domains(),
                {"x": frozenset({2}), "y": frozenset({1, 3}), "z": frozenset({1, 3})},
            )

            with solver.assume("y", 1):
                self.assertTrue(solver.propagate())
                self.assertEqual(
                    solver.domains(),
                    {"x": frozenset({2}), "y": frozenset({1}), "z": frozenset({3})},
                )
                inner_removals = {
                    (item.variable, item.value)
                    for item in solver.domains_store.removals_by_level()[2]
                }
                self.assertEqual(inner_removals, {("y", 3), ("z", 1)})

            self.assertEqual(
                solver.domains(),
                {"x": frozenset({2}), "y": frozenset({1, 3}), "z": frozenset({1, 3})},
            )
            self.assertNotIn(2, solver.domains_store.removals_by_level())
            self.assertEqual(solver.solve(), {"x": 2, "y": 1, "z": 3})

    def test_conflict_exit_leaves_outer_scope_solvable(self) -> None:
        solver = make_solver()
        with solver.assume("y", 3):
            with solver.assume("x", 3):
                self.assertFalse(solver.propagate())
            self.assertEqual(solver.solve(), {"x": 2, "y": 3, "z": 1})

    def test_inner_search_conflict_leaves_outer_scope_solvable(self) -> None:
        solver = make_solver()
        with solver.assume("y", 3):
            with solver.assume("x", 3):
                self.assertIsNone(solver.solve())
                self.assertEqual(solver.scope_depth, 2)
            self.assertEqual(solver.scope_depth, 1)
            self.assertEqual(solver.solve(), {"x": 2, "y": 3, "z": 1})

    def test_repeated_equal_assumptions_keep_separate_levels(self) -> None:
        solver = make_solver()
        with solver.assume("x", 2):
            self.assertEqual(solver.scope_depth, 1)
            with solver.assume("x", 2):
                self.assertEqual(solver.scope_depth, 2)
                self.assertEqual(solver.solve(), {"x": 2, "y": 1, "z": 3})
            self.assertEqual(solver.scope_depth, 1)
            self.assertEqual(solver.solve(), {"x": 2, "y": 1, "z": 3})

    def test_cancelled_scope_does_not_pollute_next_scope(self) -> None:
        solver = make_solver()
        base_domains = solver.domains()

        with solver.assume("y", 3):
            self.assertTrue(solver.propagate())
        self.assertEqual(solver.domains(), base_domains)

        with solver.assume("x", 2):
            self.assertEqual(solver.solve(), {"x": 2, "y": 1, "z": 3})
        self.assertEqual(solver.domains(), base_domains)

    def test_exception_inside_scope_rolls_back(self) -> None:
        solver = make_solver()
        before = solver.domains()
        with self.assertRaises(RuntimeError):
            with solver.assume("y", 3):
                solver.propagate()
                raise RuntimeError("stop")
        self.assertEqual(solver.domains(), before)
        self.assertEqual(solver.scope_depth, 0)

    def test_manual_cancel_scope_restores_previous_level(self) -> None:
        solver = make_solver()
        before = solver.domains()
        solver.push_scope((Assumption("y", 3),))
        self.assertTrue(solver.propagate())
        self.assertNotEqual(solver.domains(), before)
        solver.cancel_scope()
        self.assertEqual(solver.domains(), before)
        self.assertEqual(solver.scope_depth, 0)

    def test_independent_checker_uses_original_domains_and_constraints(self) -> None:
        solver = make_solver()
        domains = solver.initial_domains()
        constraints = solver.constraints()

        with solver.assume("y", 3):
            solver.propagate()
            self.assertEqual(solver.domain("y"), frozenset({3}))
            assignment_ignored_by_current_domain = {"x": 2, "y": 1, "z": 3}
            self.assertTrue(validate_assignment(
                assignment_ignored_by_current_domain, domains, constraints
            ))
            self.assertFalse(validate_assignment(
                assignment_ignored_by_current_domain,
                domains,
                constraints,
                [Assumption("y", 3)],
            ))

        self.assertFalse(validate_assignment(
            {"x": 1, "y": 2, "z": 3}, domains, constraints
        ))
        self.assertFalse(validate_assignment(
            {"x": 2, "y": 2, "z": 1}, domains, constraints
        ))
        self.assertFalse(validate_assignment(
            {"x": 2, "y": 3}, domains, constraints
        ))
        self.assertFalse(validate_assignment(
            {"x": 2, "y": 3, "z": 0}, domains, constraints
        ))

    def test_multiple_assumptions_in_one_scope(self) -> None:
        solver = make_solver()
        with solver.assuming((("x", {2, 3}), ("z", {1, 2}))):
            solution = solver.solve()
            self.assertIn(solution["x"], {2, 3})
            self.assertIn(solution["z"], {1, 2})
            self.assertTrue(validate_assignment(
                solution,
                solver.initial_domains(),
                solver.constraints(),
                [Assumption("x", {2, 3}), Assumption("z", {1, 2})],
            ))

    def test_permanent_constraint_cannot_be_added_under_assumption(self) -> None:
        solver = make_solver()
        with solver.assume("x", 2):
            with self.assertRaises(RuntimeError):
                solver.add_constraint(["y"], lambda y: y > 1)


if __name__ == "__main__":
    unittest.main()
