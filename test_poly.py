"""unittest 套件：稀疏多项式核心、外存归并、惰性求值、稠密小域参考。"""
from __future__ import annotations

import random
import unittest
from fractions import Fraction

from poly import Polynomial, TermLimitExceeded, multiply
from lazy import (
    Add,
    Budget,
    Deriv,
    Leaf,
    Mul,
    Partial,
    evaluate,
    evaluate_full,
    leaf,
    resume,
)
from dense_ref import DenseModP, sparse_mod_p

PRIME = 101  # 小域特征


def random_poly(rng: random.Random, nvars: int, nterms: int,
                max_deg: int = 3, coef_bound: int = 5) -> Polynomial:
    terms = {}
    for _ in range(nterms):
        exp = tuple(rng.randint(0, max_deg) for _ in range(nvars))
        coef = Fraction(rng.randint(-coef_bound, coef_bound),
                        rng.randint(1, 4))
        if coef:
            terms[exp] = coef
    return Polynomial(nvars, terms)


class TestCore(unittest.TestCase):
    def test_combine_like_terms_and_canonical_order(self):
        # 重复单项式精确合并：x0 + x0 = 2*x0；输出按次数降序 canonical
        p = Polynomial(2, [((1, 0), 1), ((1, 0), Fraction(1)), ((0, 3), 2),
                           ((0, 0), -1)])
        self.assertEqual(p.term_count, 3)
        self.assertEqual(p._terms[(1, 0)], Fraction(2))
        exps = [e for e, _ in p.sorted_terms()]
        self.assertEqual(exps, [(0, 3), (1, 0), (0, 0)])  # 次数降序

    def test_zero_term_reclaimed_on_construction(self):
        p = Polynomial(2, [((1, 1), 1), ((1, 1), -1), ((0, 0), 0)])
        self.assertTrue(p.is_zero())
        self.assertEqual(p.to_string(), "0")

    def test_x_minus_x_is_zero(self):
        x = Polynomial.variable(0, 2)
        self.assertTrue((x - x).is_zero())
        self.assertEqual((x - x).term_count, 0)

    def test_negative_exponent_rejected(self):
        with self.assertRaises(ValueError):
            Polynomial(2, {(-1, 0): 1})
        with self.assertRaises(ValueError):
            Polynomial(1, [((2,), 1), ((-3,), 2)])

    def test_zero_variable_constant_defined(self):
        c = Polynomial.constant(Fraction(3, 4), 0)
        self.assertEqual(c.nvars, 0)
        self.assertEqual(c._terms, {(): Fraction(3, 4)})
        d = c + c
        self.assertEqual(d._terms, {(): Fraction(3, 2)})
        e = c * c
        self.assertEqual(e._terms, {(): Fraction(9, 16)})
        self.assertTrue((c - c).is_zero())
        with self.assertRaises(ValueError):
            c.deriv(0)  # 零变量无求导下标

    def test_nvars_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            Polynomial.variable(0, 1) + Polynomial.variable(0, 2)

    def test_add_associative_commutative(self):
        rng = random.Random(7)
        for _ in range(50):
            a = random_poly(rng, 3, 6)
            b = random_poly(rng, 3, 6)
            c = random_poly(rng, 3, 6)
            self.assertEqual(a + b, b + a)
            self.assertEqual((a + b) + c, a + (b + c))
            self.assertTrue((a - a).is_zero())

    def test_mul_cross_terms_merge(self):
        # (x + y) * (x - y) = x^2 - y^2：交叉项 +xy 与 -xy 精确抵消回收
        x = Polynomial.variable(0, 2)
        y = Polynomial.variable(1, 2)
        prod = (x + y) * (x - y)
        self.assertEqual(prod.term_count, 2)
        self.assertEqual(prod._terms, {(2, 0): Fraction(1), (0, 2): Fraction(-1)})

    def test_mul_known_value_and_distributive(self):
        rng = random.Random(11)
        for _ in range(50):
            a = random_poly(rng, 3, 5)
            b = random_poly(rng, 3, 5)
            c = random_poly(rng, 3, 5)
            self.assertEqual(a * (b + c), a * b + a * c)
            self.assertEqual(a * b, b * a)

    def test_deriv_known_value(self):
        # p = 3*x0^2*x1 + (1/2)*x1^3 + 2
        p = Polynomial(2, {(2, 1): Fraction(3), (0, 3): Fraction(1, 2),
                           (0, 0): 2})
        dp0 = p.deriv(0)
        self.assertEqual(dp0._terms, {(1, 1): Fraction(6)})
        dp1 = p.deriv(1)
        self.assertEqual(dp1._terms, {(2, 0): Fraction(3),
                                      (0, 2): Fraction(3, 2)})

    def test_deriv_commutes(self):
        rng = random.Random(13)
        for _ in range(50):
            p = random_poly(rng, 3, 8)
            for i, j in ((0, 1), (1, 2), (0, 2)):
                self.assertEqual(p.deriv(i).deriv(j), p.deriv(j).deriv(i))

    def test_leibniz_product_rule(self):
        rng = random.Random(17)
        for _ in range(30):
            a = random_poly(rng, 2, 5)
            b = random_poly(rng, 2, 5)
            for v in (0, 1):
                self.assertEqual((a * b).deriv(v),
                                 a.deriv(v) * b + a * b.deriv(v))


class TestExternalMerge(unittest.TestCase):
    def test_external_matches_inplace(self):
        rng = random.Random(23)
        for _ in range(20):
            a = random_poly(rng, 3, 12)
            b = random_poly(rng, 3, 12)
            expected = multiply(a, b, mem_budget=1 << 30)
            for budget in (1, 3, 7, 50):
                got = multiply(a, b, mem_budget=budget)
                self.assertEqual(got, expected)
                self.assertEqual(got.to_string(), expected.to_string())

    def test_external_zero_reclaim(self):
        # 构造大量交叉抵消：(x+y)^k 与 (x-y)^k 类组合的乘积含大量重复单项式
        a = Polynomial(2, {(1, 0): 1, (0, 1): 1})
        b = Polynomial(2, {(1, 0): 1, (0, 1): -1})
        prod = multiply(a, b, mem_budget=1)  # 强制外存路径
        self.assertEqual(prod._terms, {(2, 0): Fraction(1),
                                       (0, 2): Fraction(-1)})

    def test_external_spill_dir_cleaned(self):
        import os, tempfile
        rng = random.Random(29)
        a = random_poly(rng, 2, 10)
        b = random_poly(rng, 2, 10)
        with tempfile.TemporaryDirectory() as d:
            multiply(a, b, mem_budget=2, spill_dir=d)
            self.assertEqual(os.listdir(d), [])  # 归并段已回收


class TestLazy(unittest.TestCase):
    def _sample_expr(self):
        rng = random.Random(31)
        a = random_poly(rng, 3, 6)
        b = random_poly(rng, 3, 6)
        c = random_poly(rng, 3, 6)
        # ((a*b) + c) 的偏导 + a
        expr = Add(Deriv(Add(Mul(leaf(a), leaf(b)), leaf(c)), 1), leaf(a))
        direct = (a * b + c).deriv(1) + a
        return expr, direct

    def test_budget_exhausted_returns_resumable_not_truncated(self):
        expr, direct = self._sample_expr()
        result = evaluate(expr, Budget(quota=2))
        self.assertIsInstance(result, Partial)
        # Partial 不是截断：acc + resume(rest) 必须精确收敛到完整结果
        final, peak, slices = evaluate_full(expr, quota=2)
        self.assertEqual(final, direct)
        self.assertGreater(slices, 1)
        self.assertGreater(peak, 0)

    def test_random_pause_resume_canonical_identical(self):
        rng = random.Random(37)
        expr, direct = self._sample_expr()
        canonical = direct.to_string()
        for trial in range(30):
            budget = Budget(quota=rng.randint(1, 9))
            result = evaluate(expr, budget)
            peak = budget.peak
            slices = 1
            while isinstance(result, Partial):
                budget = Budget(quota=rng.randint(1, 9))
                result = resume(result, budget)
                peak = max(peak, budget.peak)
                slices += 1
                self.assertLess(slices, 10000, "恢复循环必须终止")
            self.assertEqual(result, direct)
            self.assertEqual(result.to_string(), canonical)
        # 记录峰值项数（供报告）
        self.peak_record = peak

    def test_deriv_node_distributes_over_partial(self):
        rng = random.Random(41)
        a = random_poly(rng, 2, 8)
        b = random_poly(rng, 2, 8)
        expr = Deriv(Mul(leaf(a), leaf(b)), 0)
        final, _, _ = evaluate_full(expr, quota=1)
        self.assertEqual(final, (a * b).deriv(0))

    def test_term_inflation_hard_limit(self):
        rng = random.Random(43)
        a = random_poly(rng, 2, 8, max_deg=4)
        b = random_poly(rng, 2, 8, max_deg=4)
        expr = Mul(leaf(a), leaf(b))
        full = a * b
        if full.term_count > 3:
            with self.assertRaises(TermLimitExceeded):
                evaluate_full(expr, quota=4, max_terms=3)
        # 上限足够时正常完成
        final, _, _ = evaluate_full(expr, quota=4,
                                    max_terms=full.term_count + 5)
        self.assertEqual(final, full)

    def test_lazy_matches_eager_random(self):
        rng = random.Random(47)
        for _ in range(30):
            a = random_poly(rng, 3, 5)
            b = random_poly(rng, 3, 5)
            c = random_poly(rng, 3, 5)
            expr = Add(Mul(leaf(a), Add(leaf(b), leaf(c))), leaf(a))
            final, _, _ = evaluate_full(expr, quota=rng.randint(1, 5))
            self.assertEqual(final, a * (b + c) + a)


class TestDenseReference(unittest.TestCase):
    """独立稠密 GF(p) 参考：交叉检验稀疏实现的运算与偏导交换关系。"""

    MAX_DEG = 8

    def _project_pair(self, a, b):
        da = DenseModP.from_sparse(a, PRIME, self.MAX_DEG)
        db = DenseModP.from_sparse(b, PRIME, self.MAX_DEG)
        return da, db

    def test_add_mul_crosscheck(self):
        rng = random.Random(53)
        for _ in range(40):
            a = random_poly(rng, 3, 6, max_deg=2)
            b = random_poly(rng, 3, 6, max_deg=2)
            da, db = self._project_pair(a, b)
            self.assertEqual(sparse_mod_p(a + b, PRIME), (da + db).to_dict())
            self.assertEqual(sparse_mod_p(a * b, PRIME), (da * db).to_dict())
            self.assertEqual(sparse_mod_p(a - b, PRIME),
                             (da + DenseModP(3, self.MAX_DEG, PRIME,
                                             [(-x) % PRIME for x in db.data])
                              ).to_dict())

    def test_deriv_crosscheck_and_commutation(self):
        rng = random.Random(59)
        for _ in range(40):
            p = random_poly(rng, 3, 7, max_deg=3)
            d = DenseModP.from_sparse(p, PRIME, self.MAX_DEG)
            for v in range(3):
                self.assertEqual(sparse_mod_p(p.deriv(v), PRIME),
                                 d.deriv(v).to_dict())
            # 偏导交换关系：稀疏侧与稠密参考侧分别验证
            for i, j in ((0, 1), (1, 2), (0, 2)):
                self.assertEqual(
                    sparse_mod_p(p.deriv(i).deriv(j), PRIME),
                    d.deriv(i).deriv(j).to_dict(),
                )
                self.assertEqual(d.deriv(i).deriv(j), d.deriv(j).deriv(i))

    def test_dense_zero_variable(self):
        c = Polynomial.constant(Fraction(5, 2), 0)
        d = DenseModP.from_sparse(c, PRIME, 0)
        self.assertEqual(d.data, [(5 * pow(2, -1, PRIME)) % PRIME])
        self.assertEqual((d + d).to_dict(),
                         sparse_mod_p(c + c, PRIME))


if __name__ == "__main__":
    unittest.main(verbosity=2)
