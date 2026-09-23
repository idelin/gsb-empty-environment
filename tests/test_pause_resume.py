"""随机暂停/恢复测试：任意断点下规范输出相同，并记录峰值项数。"""

import random
import unittest
from fractions import Fraction

from sparsepoly import (Add, Budget, Deriv, Expr, Leaf, Mul, Polynomial,
                        evaluate_fully)


def random_poly(rng, nvars, max_terms=5, max_deg=3):
    terms = {}
    for _ in range(rng.randint(1, max_terms)):
        exp = tuple(rng.randint(0, max_deg) for _ in range(nvars))
        terms[exp] = Fraction(rng.randint(-5, 5) or 1, rng.randint(1, 4))
    return Polynomial(nvars, terms)


def random_expr(rng, nvars, depth):
    if depth == 0:
        return Leaf(random_poly(rng, nvars))
    op = rng.choice("amd")
    if op == "a":
        return Add(random_expr(rng, nvars, depth - 1),
                   random_expr(rng, nvars, depth - 1))
    if op == "m":
        return Mul(random_expr(rng, nvars, depth - 1),
                   random_expr(rng, nvars, depth - 1))
    return Deriv(random_expr(rng, nvars, depth - 1), rng.randrange(nvars))


def evaluate_with_random_pauses(expr, rng, max_steps_per_call=6):
    """每步用随机小步数预算求值，统计暂停次数与峰值项数。"""
    node = expr
    pauses = 0
    peak = 0
    while isinstance(node, Expr):
        budget = Budget(max_live_terms=100_000,
                        max_steps=rng.randint(1, max_steps_per_call),
                        allow_external=False)
        node = node.evaluate(budget)
        peak = max(peak, budget.peak_live_terms)
        if isinstance(node, Expr):
            pauses += 1
    return node, pauses, peak


class TestPauseResume(unittest.TestCase):
    def test_canonical_output_invariant_under_random_pauses(self):
        build_rng = random.Random(7)
        total_pauses = 0
        for case in range(8):
            nvars = build_rng.randint(1, 3)
            expr = random_expr(build_rng, nvars, depth=3)
            reference = evaluate_fully(expr, Budget(allow_external=False))
            canonical = reference.sorted_terms()
            for pause_seed in range(5):
                pause_rng = random.Random(1000 * case + pause_seed)
                got, pauses, peak = evaluate_with_random_pauses(expr, pause_rng)
                total_pauses += pauses
                self.assertEqual(got.sorted_terms(), canonical,
                                 f"case={case} pause_seed={pause_seed}")
                # 峰值项数至少覆盖最终规范形式的项数
                self.assertGreaterEqual(peak, len(reference.terms))
        self.assertGreater(total_pauses, 0)  # 整体上确实发生了暂停/恢复

    def test_peak_terms_recorded(self):
        rng = random.Random(42)
        expr = random_expr(rng, 2, depth=3)
        budget = Budget(allow_external=True)
        result = evaluate_fully(expr, budget)
        self.assertGreaterEqual(budget.peak_live_terms, len(result.terms))
        self.assertGreater(budget.peak_live_terms, 0)

    def test_pause_resume_with_external_spill(self):
        # 小内存额度 + 随机步数：溢写与暂停交织，结果仍一致
        build_rng = random.Random(99)
        f = random_poly(build_rng, 2, max_terms=8, max_deg=4)
        g = random_poly(build_rng, 2, max_terms=8, max_deg=4)
        reference = f * g
        for seed in range(5):
            rng = random.Random(seed)
            node = Mul(Leaf(f), Leaf(g))
            while isinstance(node, Expr):
                budget = Budget(max_live_terms=rng.randint(2, 6),
                                max_steps=rng.randint(1, 30),
                                allow_external=True)
                node = node.evaluate(budget)
            self.assertEqual(node.sorted_terms(), reference.sorted_terms())


if __name__ == "__main__":
    unittest.main()
