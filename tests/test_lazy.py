"""惰性求值测试：与即时求值等价、外存归并、预算不足返回续算表达式。"""

import os
import tempfile
import unittest
from fractions import Fraction

from sparsepoly import (Add, Budget, Deriv, Expr, Leaf, Mul, Polynomial,
                        evaluate_fully)


def sample_polys():
    x = Polynomial.variable(0, 2)
    y = Polynomial.variable(1, 2)
    f = (x + y) * (x + y) + Polynomial.constant(Fraction(1, 2), 2)
    g = x * y + y - Polynomial.constant(3, 2)
    return x, y, f, g


class TestLazyEquivalence(unittest.TestCase):
    def test_lazy_matches_eager(self):
        _, _, f, g = sample_polys()
        expr = Add(Mul(Leaf(f), Leaf(g)), Deriv(Leaf(f), 0))
        eager = f * g + f.partial(0)
        got = evaluate_fully(expr, Budget())
        self.assertEqual(got, eager)

    def test_mixed_polynomial_and_expr_children(self):
        x, y, f, _ = sample_polys()
        expr = Mul(Add(x, Leaf(y)), Leaf(f))  # Polynomial 可直接作子节点
        got = evaluate_fully(expr, Budget())
        self.assertEqual(got, (x + y) * f)

    def test_nested_deriv(self):
        _, _, f, _ = sample_polys()
        expr = Deriv(Deriv(Leaf(f), 0), 1)
        got = evaluate_fully(expr, Budget())
        self.assertEqual(got, f.partial(0).partial(1))


class TestExternalMerge(unittest.TestCase):
    def test_spill_and_merge_matches_direct(self):
        _, _, f, g = sample_polys()
        big1 = f * f * f  # 具有一定规模
        big2 = g * g * g
        direct = big1 * big2
        with tempfile.TemporaryDirectory() as tmp:
            budget = Budget(max_live_terms=3, allow_external=True, spill_dir=tmp)
            got = evaluate_fully(Mul(Leaf(big1), Leaf(big2)), budget)
            self.assertEqual(got, direct)
            self.assertGreaterEqual(budget.spilled_runs, 1)  # 确实走了外存
            self.assertEqual(os.listdir(tmp), [])  # 归并后临时文件已清理
            self.assertGreaterEqual(budget.peak_live_terms, len(direct.terms))

    def test_zero_cancellation_across_runs(self):
        # p*q 与 (-p)*q 相加，跨运行段抵消为零项并被回收
        x, y, _, _ = sample_polys()
        p = x + y
        q = x * x + y
        r = evaluate_fully(
            Add(Mul(Leaf(p), Leaf(q)), Mul(Leaf(-p), Leaf(q))),
            Budget(max_live_terms=2, allow_external=True),
        )
        self.assertTrue(r.is_zero())


class TestBudgetShortfall(unittest.TestCase):
    def test_insufficient_budget_returns_continuable_expr(self):
        _, _, f, g = sample_polys()
        expr = Mul(Leaf(f), Leaf(g))
        # 内存额度 1 且不允许外存：无法完成，必须返回续算表达式而非截断结果
        node = expr.evaluate(Budget(max_live_terms=1, allow_external=False))
        self.assertIsInstance(node, Expr)
        self.assertNotIsInstance(node, Polynomial)
        # 用充足预算恢复，结果与直接乘法一致
        got = evaluate_fully(node, Budget(max_live_terms=10_000,
                                          allow_external=False))
        self.assertEqual(got, f * g)

    def test_step_budget_pauses_and_resumes(self):
        _, _, f, g = sample_polys()
        expr = Mul(Leaf(f), Leaf(g))
        node = expr.evaluate(Budget(max_steps=2, allow_external=False))
        self.assertIsInstance(node, Expr)  # 步数耗尽 -> 暂停
        got = evaluate_fully(node, Budget(allow_external=False))
        self.assertEqual(got, f * g)


if __name__ == "__main__":
    unittest.main()
