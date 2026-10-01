import unittest
from fractions import Fraction

from budget_coverage import (
    Problem,
    exact_solve,
    greedy_cover,
    make_witness,
    validate_witness,
)


def demo():
    # D 成本超预算，永远不会出现在可行解中
    return Problem.create(
        element_weights=[5, 4, 4],
        set_ids=list("ABCD"),
        set_costs=[0, 3, 2, 9],
        set_elements=[{0}, {1}, {2}, {0}],
        budget=5,
    )


class TestWitness(unittest.TestCase):
    def test_valid_witness_passes(self):
        p = demo()
        r = exact_solve(p)
        rep = validate_witness(p, make_witness(p, r, "exact"))
        self.assertTrue(rep.ok)
        self.assertFalse(rep.stale)

    def test_tampered_profit_fails(self):
        p = demo()
        r = exact_solve(p)
        w = make_witness(p, r, "exact")
        w.claimed_profit += 1
        rep = validate_witness(p, w)
        self.assertFalse(rep.ok)
        self.assertTrue(any("收益不符" in m for m in rep.problems))

    def test_optimality_void_after_unrelated_delete(self):
        p = demo()
        r = exact_solve(p)
        w = make_witness(p, r, "exact")
        p.delete_set("D")  # D 不在最优解中
        rep = validate_witness(p, w)
        self.assertTrue(rep.stale)
        self.assertFalse(rep.ok)  # 旧最优性结论失效
        self.assertEqual(rep.recomputed_profit, Fraction(13))  # 解本身仍可行
        r2 = exact_solve(p)
        self.assertEqual(r2.profit, Fraction(13))
        self.assertTrue(validate_witness(p, make_witness(p, r2, "exact")).ok)

    def test_witness_invalid_when_selected_set_deleted(self):
        p = demo()
        r = exact_solve(p)
        w = make_witness(p, r, "exact")
        p.delete_set("B")
        rep = validate_witness(p, w)
        self.assertFalse(rep.ok)
        self.assertTrue(any("已不存在" in m for m in rep.problems))

    def test_feasible_witness_survives_unrelated_delete(self):
        p = demo()
        g = greedy_cover(p)
        w = make_witness(p, g, "greedy")
        p.delete_set("D")
        rep = validate_witness(p, w)
        self.assertTrue(rep.stale)
        self.assertTrue(rep.ok)  # 可行解见证仍成立（本就不含最优性承诺）


if __name__ == "__main__":
    unittest.main()
