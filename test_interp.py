"""rational_interp 的单元测试（python3 -m unittest -v）。"""

import unittest
from fractions import Fraction as F

from rational_interp import (
    BitTracker, DuplicateConflictError, Interpolator, Poly,
    StructureError, UnknownObservationError, linear_solve)


def P(*coeffs):
    return Poly(list(coeffs))


class TestPoly(unittest.TestCase):
    def test_canonical_and_eval(self):
        self.assertEqual(P(0, 0, 0).coeffs, [0])
        p = P(F(1, 2), F(-1, 3), 2)
        self.assertEqual(p.eval(2), F(1, 2) - F(2, 3) + 8)

    def test_derivatives(self):
        self.assertEqual(P(3, 2, 1).derivative(), P(2, 2))      # 2+2x
        self.assertEqual(P(1, 0, 0, 1).derivative(3), P(6))    # x^3 -> 6
        self.assertEqual(P(5).derivative(2), P(0))
        self.assertEqual(P(1, 0, 0, 1).eval_derivative(2, 2), 12)

    def test_mul_linear_and_newton(self):
        # (x-1)(x-2)(x-4) = x^3 - 7x^2 + 14x - 8
        p = Poly([1]).mul_by_linear(1).mul_by_linear(2).mul_by_linear(4)
        self.assertEqual(p, P(-8, 14, -7, 1))
        q = Poly.from_newton([1, 2, 4], [7, 2, F(1, 2)])
        self.assertEqual(q.eval(1), 7)
        self.assertEqual(q.eval(4), 7 + 2 * 3 + F(1, 2) * 3 * 2)


class TestLagrange(unittest.TestCase):
    def test_quadratic_rational(self):
        itp = Interpolator()
        for x, y in [(1, 1), (2, F(1, 2)), (4, F(1, 4))]:
            itp.observe(x, y)
        p = itp.polynomial()
        self.assertEqual(p, P(F(7, 4), F(-7, 8), F(1, 8)))
        self.assertTrue(itp.verify().ok)
        for x, y in [(1, 1), (2, F(1, 2)), (4, F(1, 4))]:
            self.assertEqual(p.eval(x), F(y))

    def test_recovers_cubic_with_extra_node(self):
        itp = Interpolator()
        pts = [(-2, -8), (-1, -1), (0, 0), (1, 1), (2, 8)]
        for x, y in pts:
            itp.observe(x, y)
        self.assertEqual(itp.polynomial(), P(0, 0, 0, 1))
        self.assertEqual(itp.verify().degree, 3)  # 最高次系数为 0 -> 次数 3

    def test_newton_and_monomial_agree(self):
        itp = Interpolator()
        for x, y in [(0, 5), (F(1, 2), 7), (3, 2), (F(-1, 3), 0)]:
            itp.observe(x, y)
        nodes, coeffs = itp.newton_form()
        self.assertEqual(Poly.from_newton(nodes, coeffs), itp.polynomial())
        self.assertTrue(itp.verify().ok)


class TestDuplicates(unittest.TestCase):
    def test_same_value_idempotent(self):
        itp = Interpolator()
        itp.observe(1, 2)
        itp.observe(1, 2)
        self.assertEqual(len(itp.observations), 1)
        self.assertEqual(itp.seq, [(F(1), 0)])

    def test_conflicting_value(self):
        itp = Interpolator()
        itp.observe(1, 2)
        with self.assertRaises(DuplicateConflictError) as cm:
            itp.observe(1, 3)
        self.assertEqual(cm.exception.existing, 2)
        self.assertEqual(cm.exception.incoming, 3)
        self.assertEqual(len(itp.observations), 1)  # 冲突观测未写入

    def test_conflicting_derivative(self):
        itp = Interpolator()
        itp.observe(1, 1, 0)
        itp.observe(1, 2, 1)
        with self.assertRaises(DuplicateConflictError):
            itp.observe(1, 5, 1)

    def test_missing_lower_order(self):
        itp = Interpolator()
        with self.assertRaises(StructureError):
            itp.observe(0, 1, derivative_order=2)
        itp.observe(0, 0, 0)
        with self.assertRaises(StructureError):
            itp.observe(0, 3, derivative_order=2)  # 缺 1 阶


class TestHermite(unittest.TestCase):
    def test_hermite_recovers_polynomial(self):
        h = Interpolator()
        h.observe(0, 0, 0)
        h.observe(0, 0, 1)
        h.observe(0, 0, 2)
        h.observe(1, 1, 0)
        h.observe(1, 3, 1)
        h.observe(2, 8, 0)
        self.assertEqual(h.polynomial(), P(0, 0, 0, 1))
        v = h.verify()
        self.assertTrue(v.ok)
        self.assertEqual(h.nodes_in_order, [F(0), F(0), F(0), F(1), F(1), F(2)])

    def test_hermite_mixed_rational(self):
        # P(x) = 1/2 + 1/3 x + 1/4 x(x-1) 的构造与逐点核验
        h = Interpolator()
        target = Poly.from_newton([F(1), F(1), F(2)],
                                  [F(1, 2), F(1, 3), F(1, 4)])
        h.observe(1, target.eval(1), 0)
        h.observe(1, target.eval_derivative(1, 1), 1)
        h.observe(2, target.eval(2), 0)
        self.assertEqual(h.polynomial(), target)
        self.assertTrue(h.verify().ok)
        report = h.reference_check()
        self.assertTrue(report.agree)


class TestIncrementalForget(unittest.TestCase):
    def test_forget_interior_rebuilds_suffix(self):
        w = Interpolator()
        for x, y in [(0, 1), (1, 3), (2, 9), (3, 27)]:
            w.observe(x, y)
        before = w.newton_coefficients
        w.forget(2)
        after = w.newton_coefficients
        self.assertEqual(len(after), 3)
        self.assertEqual(after[:2], before[:2])      # 前缀复用
        self.assertNotEqual(after[2], before[2])     # 后缀重算，非删末项
        expect = Interpolator()
        for x, y in [(0, 1), (1, 3), (3, 27)]:
            expect.observe(x, y)
        self.assertEqual(w.polynomial(), expect.polynomial())
        self.assertTrue(w.verify().ok)

    def test_forget_last_then_readd(self):
        w = Interpolator()
        for x, y in [(0, 1), (1, 2), (2, 5)]:
            w.observe(x, y)
        w.forget(2)
        self.assertEqual(len(w.seq), 2)
        w.observe(2, 5)
        self.assertEqual(w.polynomial(), P(1, 0, 1))
        self.assertTrue(w.verify().ok)

    def test_forget_unknown(self):
        w = Interpolator()
        with self.assertRaises(UnknownObservationError):
            w.forget(7)

    def test_forget_value_with_derivatives_needs_cascade(self):
        w = Interpolator()
        w.observe(1, 1)
        w.observe(1, 2, 1)
        with self.assertRaises(StructureError):
            w.forget(1)
        w.forget(1, cascade=True)
        self.assertEqual(w.observations, {})

    def test_forget_then_observe_derivative(self):
        w = Interpolator()
        w.observe(0, 0)
        w.observe(0, 1, 1)
        w.observe(0, 0, 2)
        w.forget(0, derivative_order=2)  # 撤回最高阶
        self.assertEqual(set(w.observations), {(F(0), 0), (F(0), 1)})
        w.observe(0, 0, 2)
        self.assertTrue(w.verify().ok)

    def test_forget_then_empty(self):
        w = Interpolator()
        w.observe(1, 9)
        w.forget(1)
        self.assertEqual(w.polynomial(), P(0))
        self.assertTrue(w.verify().ok)


class TestOrderEquivalence(unittest.TestCase):
    def test_permutations_give_same_poly(self):
        import itertools
        pts = [(F(0), 3), (F(1), 1), (F(2), 5), (F(5), 2)]
        polys = set()
        for perm in itertools.permutations(pts):
            w = Interpolator()
            for x, y in perm:
                w.observe(x, y)
            polys.add(w.polynomial())
        self.assertEqual(len(polys), 1)

    def test_hermite_block_order_equivalence(self):
        # 不同节点块的先后顺序不改变最终多项式
        def build(order):
            w = Interpolator()
            for x, k, y in order:
                w.observe(x, y, k)
            return w.polynomial()
        a = [(-1, 0, 3), (-1, 1, 2), (2, 0, -13), (2, 1, -1)]
        b = [(2, 0, -13), (2, 1, -1), (-1, 0, 3), (-1, 1, 2)]
        self.assertEqual(build(a), build(b))
        # 四条独立约束确定唯一三次多项式（汇合 Hermite）：
        self.assertEqual(build(a),
                         P(F(-1, 27), F(-61, 9), F(-22, 9), F(35, 27)))
        self.assertTrue(build(a).eval(F(2)) == -13)


class TestLinearSolver(unittest.TestCase):
    def test_unique_solution(self):
        A = [[F(2), F(1)], [F(1), F(-1)]]
        r = linear_solve(A, [F(1), F(2)])
        self.assertTrue(r.consistent)
        self.assertEqual(r.solution, [F(1), F(-1)])

    def test_overdetermined_consistent(self):
        A = [[F(1), F(0)], [F(0), F(1)], [F(1), F(1)]]
        r = linear_solve(A, [F(3), F(4), F(7)])
        self.assertTrue(r.consistent)
        self.assertEqual(r.solution, [F(3), F(4)])

    def test_inconsistent_gives_witness(self):
        A = [[F(1), x] for x in (1, 2, 4)] + [[F(1), F(4)]]
        b = [F(1), F(1, 2), F(1, 4), F(1, 5)]
        r = linear_solve(A, b)
        self.assertFalse(r.consistent)
        self.assertIsNotNone(r.witness)
        zero_row = [sum(wi * ai for wi, ai in zip(r.witness, col))
                    for col in zip(*A)]
        self.assertTrue(all(z == 0 for z in zero_row))
        self.assertNotEqual(sum(wi * bi for wi, bi in zip(r.witness, b)), 0)

    def test_underdetermined(self):
        r = linear_solve([[F(1), F(1), F(0)]], [F(2)])
        self.assertTrue(r.consistent)
        self.assertEqual(r.solution, [F(2), F(0), F(0)])

    def test_witness_equation_render(self):
        itp = Interpolator()
        for x, y in [(0, 1), (1, 3), (2, 9)]:
            itp.observe(x, y)
        A, b, keys, d = itp._hermite_system()
        from rational_interp import ReferenceReport
        keys = keys + [(F(3), 0)]
        res = linear_solve(A + [[F(1), F(3), F(9)]], b + [F(100)])
        rep = ReferenceReport(False, None, itp.polynomial(), False,
                              res.witness, keys)
        eq = rep.witness_equation()
        self.assertIn("p(3)", eq)
        self.assertIn("!= 0", eq)

    def test_reference_check_matches(self):
        w = Interpolator()
        for x, y in [(F(1, 2), F(1, 3)), (F(2, 3), F(5, 2)),
                     (F(3), F(-1, 7)), (4, 8)]:
            w.observe(x, y)
        rep = w.reference_check()
        self.assertTrue(rep.consistent)
        self.assertTrue(rep.agree)
        self.assertTrue(w.verify().ok)


class TestBitGrowth(unittest.TestCase):
    def test_tracks_growth(self):
        w = Interpolator()
        for i, y in enumerate([F(1), F(1, 7), F(2, 13), F(9, 5), F(4, 11)]):
            w.observe(i, y)
        w.reference_check()
        rep = w.bits.report()
        for stage in ("inputs", "divided_differences",
                      "reference_matrix", "reference_solution"):
            self.assertIn(stage, rep)
            nb, db = rep[stage]
            self.assertGreaterEqual(nb, 1)
            self.assertGreaterEqual(db, 1)
        # 精确性：即便位长增长，全部残差必须严格为 0
        self.assertTrue(w.verify().ok)

    def test_bit_tracker_poly(self):
        bt = BitTracker()
        bt.record("s", [P(F(1, 3), F(-7, 2))])
        nb, db = bt.report()["s"]
        self.assertEqual(db, 2)   # 分母 3 -> 2 bit
        self.assertGreaterEqual(nb, 3)


if __name__ == "__main__":
    unittest.main()
