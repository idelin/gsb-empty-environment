import unittest

from setsolver import Conflict, Solver, check_solution


class VarBoundTests(unittest.TestCase):
    def test_required_not_subset_of_possible_is_conflict(self):
        s = Solver()
        s.add_var("X", universe={1, 2, 3}, required={1, 2, 3},
                  possible={1, 2})
        with self.assertRaises(Conflict):
            s.propagate()

    def test_three_required_but_max_card_two_is_conflict(self):
        s = Solver()
        s.add_var("X", universe={1, 2, 3, 4}, required={1, 2, 3},
                  max_card=2)
        with self.assertRaises(Conflict):
            s.propagate()

    def test_min_card_exceeds_possible_count_is_conflict(self):
        s = Solver()
        s.add_var("X", universe={1, 2, 3, 4}, possible={1, 2}, min_card=3)
        with self.assertRaises(Conflict):
            s.propagate()

    def test_min_card_equals_ub_size_forces_all_required(self):
        s = Solver()
        s.add_var("X", universe={1, 2, 3, 4}, possible={1, 2}, min_card=2)
        s.propagate()
        self.assertEqual(s.vars["X"].lb, {1, 2})

    def test_max_card_equals_lb_size_forbids_the_rest(self):
        s = Solver()
        s.add_var("X", universe={1, 2, 3, 4}, required={1}, max_card=1)
        s.propagate()
        self.assertEqual(s.vars["X"].ub, {1})


class SubsetTests(unittest.TestCase):
    def test_lb_flows_forward(self):
        s = Solver()
        s.add_var("A", universe={1, 2, 3}, required={1})
        s.add_var("B", universe={1, 2, 3})
        s.add_subset("A", "B")
        s.propagate()
        self.assertIn(1, s.vars["B"].lb)

    def test_ub_flows_backward(self):
        s = Solver()
        s.add_var("A", universe={1, 2, 3})
        s.add_var("B", universe={1, 2, 3}, possible={1, 2})
        s.add_subset("A", "B")
        s.propagate()
        self.assertEqual(s.vars["A"].ub, {1, 2})

    def test_cardinality_flows(self):
        s = Solver()
        s.add_var("A", universe=range(5), min_card=2)
        s.add_var("B", universe=range(5), max_card=3)
        s.add_subset("A", "B")
        s.propagate()
        self.assertEqual(s.vars["B"].min_card, 2)
        self.assertEqual(s.vars["A"].max_card, 3)


class UnionTests(unittest.TestCase):
    def test_lb_union_and_ub_union(self):
        s = Solver()
        s.add_var("A", universe=range(6), required={1}, possible={1, 2})
        s.add_var("B", universe=range(6), required={3}, possible={3, 4})
        s.add_var("C", universe=range(6))
        s.add_union("C", "A", "B")
        s.propagate()
        C = s.vars["C"]
        self.assertEqual(C.lb, {1, 3})
        self.assertEqual(C.ub, {1, 2, 3, 4})

    def test_required_in_union_forced_into_the_other_operand(self):
        s = Solver()
        s.add_var("X", universe={1, 2, 3}, possible={1, 3})  # 2 forbidden
        s.add_var("Y", universe={1, 2, 3})
        s.add_var("Z", universe={1, 2, 3}, required={2})
        s.add_union("Z", "X", "Y")
        s.propagate()
        self.assertIn(2, s.vars["Y"].lb)

    def test_union_cardinality_conflict(self):
        s = Solver()
        s.add_var("A", universe=range(4), min_card=3)
        s.add_var("B", universe=range(4))
        s.add_var("C", universe=range(4), max_card=2)
        s.add_union("C", "A", "B")
        with self.assertRaises(Conflict):
            s.propagate()


class IntersectionTests(unittest.TestCase):
    def test_lb_and_ub_computed_separately(self):
        s = Solver()
        s.add_var("A", universe={1, 2, 3, 4}, required={1},
                  possible={1, 2, 3})
        s.add_var("B", universe={1, 2, 3, 4}, required={2},
                  possible={1, 2})
        s.add_var("C", universe={1, 2, 3, 4})
        s.add_intersection("C", "A", "B")
        s.propagate()
        C = s.vars["C"]
        self.assertEqual(C.lb, set())          # lb(A) ∩ lb(B) = ∅
        self.assertEqual(C.ub, {1, 2})         # ub(A) ∩ ub(B)

    def test_possible_members_are_not_required(self):
        # 3 is possible in both A and B but required in neither:
        # it must NOT appear in lb(C).
        s = Solver()
        s.add_var("A", universe={1, 2, 3}, required={1})
        s.add_var("B", universe={1, 2, 3}, required={2})
        s.add_var("C", universe={1, 2, 3})
        s.add_intersection("C", "A", "B")
        s.propagate()
        self.assertNotIn(3, s.vars["C"].lb)
        self.assertIn(3, s.vars["C"].ub)

    def test_required_in_both_is_required_in_intersection(self):
        s = Solver()
        s.add_var("A", universe={1, 2}, required={1})
        s.add_var("B", universe={1, 2}, required={1})
        s.add_var("C", universe={1, 2})
        s.add_intersection("C", "A", "B")
        s.propagate()
        self.assertEqual(s.vars["C"].lb, {1})

    def test_intersection_lb_conflict_with_max_card(self):
        s = Solver()
        s.add_var("A", universe={1, 2}, required={1})
        s.add_var("B", universe={1, 2}, required={1})
        s.add_var("C", universe={1, 2}, max_card=0)
        s.add_intersection("C", "A", "B")
        with self.assertRaises(Conflict):
            s.propagate()

    def test_required_but_impossible_in_other_is_forbidden(self):
        s = Solver()
        s.add_var("A", universe={1, 2, 3}, required={1})
        s.add_var("B", universe={1, 2, 3}, possible={2, 3})  # 1 impossible
        s.add_var("C", universe={1, 2, 3})
        s.add_intersection("C", "A", "B")
        s.propagate()
        self.assertNotIn(1, s.vars["C"].ub)


class SolveAndCheckTests(unittest.TestCase):
    def build_puzzle(self):
        s = Solver()
        s.add_var("X", universe={1, 2, 3, 4}, possible={1, 3, 4})
        s.add_var("Y", universe={1, 2, 3, 4}, max_card=2)
        s.add_var("Z", universe={1, 2, 3, 4}, required={1, 2}, max_card=3)
        s.add_union("Z", "X", "Y")
        s.add_subset("X", "Z")
        return s

    def test_solve_returns_verified_solution(self):
        s = self.build_puzzle()
        sol = s.solve()
        self.assertIsNotNone(sol)
        self.assertTrue(check_solution(s, sol))
        self.assertEqual(sol["Z"], sol["X"] | sol["Y"])
        self.assertIn(2, sol["Y"])

    def test_unsat_returns_none(self):
        s = Solver()
        s.add_var("X", universe={1, 2, 3}, required={1, 2, 3}, max_card=2)
        self.assertIsNone(s.solve())

    def test_checker_rejects_tampered_solution(self):
        s = self.build_puzzle()
        sol = s.solve()
        self.assertIsNotNone(sol)
        bad_card = dict(sol, Y=frozenset({1, 2, 3}))       # |Y| = 3 > 2
        self.assertFalse(check_solution(s, bad_card))
        bad_union = dict(sol, Z=frozenset({1, 2, 3, 4}))   # Z != X ∪ Y
        self.assertFalse(check_solution(s, bad_union))
        missing = {k: v for k, v in sol.items() if k != "X"}
        self.assertFalse(check_solution(s, missing))

    def test_checker_uses_declared_not_tightened_bounds(self):
        # Propagation tightens working bounds; the checker must still
        # validate against the originally declared constraints.
        s = self.build_puzzle()
        sol = s.solve()
        self.assertIsNotNone(sol)
        self.assertLessEqual(len(sol["Y"]), s.vars["Y"].declared_max)
        self.assertTrue(s.vars["Z"].declared_lb <= sol["Z"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
