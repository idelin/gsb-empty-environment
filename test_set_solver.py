import unittest

from set_solver import (
    Conflict,
    InterEq,
    Solver,
    SubsetOf,
    UnionEq,
    check_solution,
)


class BoundInvariantTests(unittest.TestCase):
    def test_lower_not_subset_of_upper_is_conflict(self):
        with self.assertRaises(Conflict):
            Solver().add_var("S", upper={1, 2, 3}, lower={1, 5})

    def test_required_three_elements_but_max_card_two_is_conflict(self):
        solver = Solver()
        with self.assertRaises(Conflict) as ctx:
            solver.add_var("S", upper={1, 2, 3, 4}, lower={1, 2, 3}, max_card=2)
        self.assertIn("max cardinality is 2", str(ctx.exception))

    def test_min_card_above_possible_elements_is_conflict(self):
        with self.assertRaises(Conflict):
            Solver().add_var("S", upper={1, 2}, min_card=3)


class CardinalityPropagationTests(unittest.TestCase):
    def test_min_card_forces_all_possible_elements(self):
        solver = Solver()
        var = solver.add_var("S", upper={1, 2, 3}, min_card=3)
        self.assertEqual(var.lower, {1, 2, 3})

    def test_max_card_excludes_everything_beyond_lower(self):
        solver = Solver()
        var = solver.add_var("S", upper={1, 2, 3}, lower={1}, max_card=1)
        self.assertEqual(var.upper, {1})

    def test_set_card_tightens_existing_var(self):
        solver = Solver()
        var = solver.add_var("S", upper={1, 2, 3})
        solver.set_card("S", min_card=3)
        self.assertEqual(var.lower, {1, 2, 3})


class SubsetPropagationTests(unittest.TestCase):
    def test_lower_flows_into_superset(self):
        solver = Solver()
        a = solver.add_var("A", upper={1, 2, 3}, lower={1})
        b = solver.add_var("B", upper={1, 2, 3})
        solver.add_constraint(SubsetOf(a, b))
        solver.propagate()
        self.assertIn(1, b.lower)

    def test_upper_of_superset_restricts_subset(self):
        solver = Solver()
        a = solver.add_var("A", upper={1, 2, 3})
        b = solver.add_var("B", upper={2, 3})
        solver.add_constraint(SubsetOf(a, b))
        solver.propagate()
        self.assertEqual(a.upper, {2, 3})

    def test_subset_conflict_when_required_element_impossible(self):
        solver = Solver()
        solver.add_var("A", upper={1, 2}, lower={1})
        solver.add_var("B", upper={2, 3})
        solver.add_constraint(SubsetOf(solver.vars["A"], solver.vars["B"]))
        with self.assertRaises(Conflict):
            solver.propagate()


class UnionPropagationTests(unittest.TestCase):
    def test_union_bounds(self):
        solver = Solver()
        a = solver.add_var("A", upper={1, 2}, lower={1})
        b = solver.add_var("B", upper={2, 3}, lower={3})
        c = solver.add_var("C", upper={1, 2, 3, 4})
        solver.add_constraint(UnionEq(a, b, c))
        solver.propagate()
        self.assertEqual(c.lower, {1, 3})
        self.assertEqual(c.upper, {1, 2, 3})  # 4 excluded: in neither operand

    def test_union_forces_origin_of_required_element(self):
        solver = Solver()
        a = solver.add_var("A", upper={2, 3})
        b = solver.add_var("B", upper={1, 3})
        c = solver.add_var("C", upper={1, 2, 3}, lower={1})
        solver.add_constraint(UnionEq(a, b, c))
        solver.propagate()
        self.assertIn(1, b.lower)  # 1 cannot come from A, so B must provide it
        self.assertNotIn(1, a.upper)

    def test_union_conflict_when_required_element_has_no_origin(self):
        solver = Solver()
        solver.add_var("A", upper={2})
        solver.add_var("B", upper={3})
        solver.add_var("C", upper={1, 2, 3}, lower={1})
        solver.add_constraint(UnionEq(solver.vars["A"], solver.vars["B"],
                                      solver.vars["C"]))
        with self.assertRaises(Conflict):
            solver.propagate()


class IntersectionPropagationTests(unittest.TestCase):
    def _make(self):
        solver = Solver()
        a = solver.add_var("A", upper={1, 2}, lower={1})
        b = solver.add_var("B", upper={1, 2, 3}, lower={2})
        c = solver.add_var("C", upper={1, 2, 3})
        solver.add_constraint(InterEq(a, b, c))
        solver.propagate()
        return solver, a, b, c

    def test_lower_and_upper_computed_separately(self):
        _, _, _, c = self._make()
        # lower: only elements required in BOTH -> empty here
        self.assertEqual(c.lower, set())
        # upper: elements possible in BOTH
        self.assertEqual(c.upper, {1, 2})

    def test_possible_members_not_treated_as_required(self):
        # 1 is possible in B and required in A; 2 is possible in A and
        # required in B. Neither is required in C.
        _, _, _, c = self._make()
        self.assertNotIn(1, c.lower)
        self.assertNotIn(2, c.lower)

    def test_required_in_both_becomes_required_in_intersection(self):
        solver = Solver()
        solver.add_var("A", upper={1, 2}, lower={1})
        solver.add_var("B", upper={1, 3}, lower={1})
        c = solver.add_var("C", upper={1, 2, 3})
        solver.add_constraint(InterEq(solver.vars["A"], solver.vars["B"], c))
        solver.propagate()
        self.assertEqual(c.lower, {1})

    def test_required_element_excluded_from_operand_without_it(self):
        solver = Solver()
        a = solver.add_var("A", upper={1, 2}, lower={1})
        b = solver.add_var("B", upper={1, 2})
        c = solver.add_var("C", upper={2})  # 1 impossible in C
        solver.add_constraint(InterEq(a, b, c))
        solver.propagate()
        # 1 is required in A but cannot be in C, so 1 cannot be in B.
        self.assertNotIn(1, b.upper)


class SolveAndVerifyTests(unittest.TestCase):
    def test_solve_returns_checker_verified_solution(self):
        solver = Solver()
        solver.add_var("A", upper={1, 2, 3, 4}, min_card=1)
        solver.add_var("B", upper={2, 3, 4, 5}, max_card=2)
        solver.add_var("C", upper={1, 2, 3, 4, 5})
        solver.add_var("D", upper={1, 2, 3, 4, 5})
        solver.add_constraint(UnionEq(solver.vars["A"], solver.vars["B"],
                                      solver.vars["C"]))
        solver.add_constraint(InterEq(solver.vars["A"], solver.vars["B"],
                                      solver.vars["D"]))
        solver.add_constraint(SubsetOf(solver.vars["D"], solver.vars["B"]))
        solution = solver.solve()
        self.assertIsNotNone(solution)
        self.assertTrue(check_solution(solver.constraints, solver._domains,
                                       solution))

    def test_unsatisfiable_problem_returns_none(self):
        solver = Solver()
        solver.add_var("A", upper={1, 2}, lower={1})
        solver.add_var("B", upper={2})
        solver.add_var("C", upper={1, 2}, max_card=0)
        solver.add_constraint(UnionEq(solver.vars["A"], solver.vars["B"],
                                      solver.vars["C"]))
        self.assertIsNone(solver.solve())

    def test_search_finds_solution_avoiding_forced_conflict(self):
        # C = A & B with C empty forces A and B disjoint; search must
        # exclude 1 from at least one of them.
        solver = Solver()
        solver.add_var("A", upper={1, 2}, lower={2})
        solver.add_var("B", upper={1, 3}, lower={3})
        solver.add_var("C", upper={1, 2, 3}, max_card=0)
        solver.add_constraint(InterEq(solver.vars["A"], solver.vars["B"],
                                      solver.vars["C"]))
        solution = solver.solve()
        self.assertIsNotNone(solution)
        self.assertFalse(1 in solution["A"] and 1 in solution["B"])
        self.assertEqual(solution["C"], frozenset())


class IndependentCheckerTests(unittest.TestCase):
    def test_checker_rejects_wrong_assignment(self):
        solver = Solver()
        solver.add_var("A", upper={1, 2}, lower={1})
        solver.add_var("B", upper={1, 2})
        solver.add_constraint(SubsetOf(solver.vars["A"], solver.vars["B"]))
        bad = {"A": frozenset({1}), "B": frozenset({2})}
        self.assertFalse(check_solution(solver.constraints, solver._domains, bad))

    def test_checker_rejects_cardinality_violation(self):
        solver = Solver()
        solver.add_var("S", upper={1, 2, 3}, max_card=1)
        bad = {"S": frozenset({1, 2})}
        self.assertFalse(check_solution([], solver._domains, bad))

    def test_checker_rejects_domain_violation(self):
        solver = Solver()
        solver.add_var("S", upper={1, 2})
        bad = {"S": frozenset({1, 9})}
        self.assertFalse(check_solution([], solver._domains, bad))


if __name__ == "__main__":
    unittest.main()
