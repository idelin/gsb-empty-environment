import unittest
from fractions import Fraction

from budget_coverage import Problem, Status, greedy_cover


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


class TestGreedy(unittest.TestCase):
    def test_zero_cost_first_no_division_by_zero(self):
        p = Problem.create(
            element_weights=[5, 3],
            set_ids=["free", "pricey"],
            set_costs=[0, 10],
            set_elements=[{0}, {1}],
            budget=1,
        )
        r = greedy_cover(p)
        self.assertEqual(r.status, Status.FEASIBLE)
        self.assertEqual(r.selected, ("free",))
        self.assertEqual(r.profit, Fraction(5))

    def test_empty_set_never_selected(self):
        p = Problem.create(
            element_weights=[5],
            set_ids=["empty", "real"],
            set_costs=[0, 1],
            set_elements=[set(), {0}],
            budget=5,
        )
        r = greedy_cover(p)
        self.assertEqual(r.selected, ("real",))

    def test_dependency_closure_selected_together(self):
        p = Problem.create(
            element_weights=[4, 4],
            set_ids=["B", "G"],
            set_costs=[2, 1],
            set_elements=[{0}, {1}],
            budget=3,
            dependencies={"G": ["B"]},
        )
        r = greedy_cover(p)
        self.assertEqual(r.status, Status.FEASIBLE)
        self.assertEqual(set(r.selected), {"B", "G"})
        self.assertEqual(r.profit, Fraction(8))

    def test_min_coverage_repair(self):
        p = Problem.create(
            element_weights=[2, 2],
            set_ids=["A", "B"],
            set_costs=[1, 1],
            set_elements=[{0}, {0, 1}],
            budget=2,
            min_coverage={0: 2},
        )
        r = greedy_cover(p)
        self.assertEqual(r.status, Status.FEASIBLE)
        self.assertEqual(set(r.selected), {"A", "B"})

    def test_greedy_never_claims_optimal(self):
        # 主实例中贪心无法满足最低覆盖：必须如实报告 UNDECIDED，
        # 不得谎称最优，也不得谎报不可行（精确参考可找到收益 21 的解）。
        r = greedy_cover(main_problem())
        self.assertEqual(r.status, Status.UNDECIDED)
        self.assertIsNone(r.profit)

    def test_greedy_status_is_never_optimal(self):
        p = Problem.create(
            element_weights=[5],
            set_ids=["A"],
            set_costs=[1],
            set_elements=[{0}],
            budget=1,
        )
        r = greedy_cover(p)
        self.assertIn(r.status, (Status.FEASIBLE, Status.UNDECIDED))


if __name__ == "__main__":
    unittest.main()
