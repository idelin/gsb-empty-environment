import unittest
from fractions import Fraction

from budget_coverage import Problem, Status, exact_solve


def main_problem():
    return Problem.create(
        element_weights=[5, 4, 4, 3, 3, 2, 2, 1],
        set_ids=list("ABCDEFG"),
        set_costs=[0, 3, 4, 2, Fraction(1, 2), 2, 2],
        set_elements=[{0, 1}, {1, 2, 3}, {2, 3, 4, 5}, {5, 6}, {6, 7},
                      set(), {3, 4}],
        budget=7,
        dependencies={"G": ["B"]},
        mutex_groups=[["C", "D"]],
        min_coverage={2: 2},
    )


class TestExact(unittest.TestCase):
    def test_main_instance(self):
        r = exact_solve(main_problem())
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(set(r.selected), {"A", "B", "C"})
        self.assertEqual(r.profit, Fraction(21))
        self.assertEqual(r.upper_bound, Fraction(21))

    def test_gap_instance(self):
        p = Problem.create(
            element_weights=[6, 5, 5],
            set_ids=["S1", "S2", "S3"],
            set_costs=[3, 3, 4],
            set_elements=[{0}, {1}, {1, 2}],
            budget=6,
        )
        r = exact_solve(p)
        self.assertEqual(r.profit, Fraction(11))
        self.assertEqual(set(r.selected), {"S1", "S2"})

    def test_infeasible_when_min_coverage_unsatisfiable(self):
        p = Problem.create(
            element_weights=[5],
            set_ids=["A"],
            set_costs=[1],
            set_elements=[{0}],
            budget=10,
            min_coverage={0: 2},
        )
        r = exact_solve(p)
        self.assertEqual(r.status, Status.INFEASIBLE)
        self.assertIsNone(r.profit)

    def test_empty_selection_optimal_when_unaffordable(self):
        p = Problem.create(
            element_weights=[5],
            set_ids=["A"],
            set_costs=[9],
            set_elements=[{0}],
            budget=1,
        )
        r = exact_solve(p)
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.selected, ())
        self.assertEqual(r.profit, Fraction(0))

    def test_enumeration_guard(self):
        p = Problem.create(
            element_weights=[1],
            set_ids=[f"s{i}" for i in range(21)],
            set_costs=[1] * 21,
            set_elements=[{0}] * 21,
            budget=30,
        )
        with self.assertRaises(ValueError):
            exact_solve(p)


if __name__ == "__main__":
    unittest.main()
