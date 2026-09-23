"""unittest 测试套件：重根、公共因子、零函数、无理排除点、组合运算。"""
import unittest
from fractions import Fraction

import polyutil as P
from rational import (
    RationalFunction, DomainError, is_excluded, normalize_exclusions,
)
from verify import verify_identity, verify_exclusions, sample_verify


def rf(num, den):
    return RationalFunction(num, den)


class TestPolyUtil(unittest.TestCase):
    def test_gcd_common_factor(self):
        a = P.mul(P.mul(P.poly([-1, 1]), P.poly([-1, 1])), P.poly([2, 1]))
        b = P.mul(P.poly([-1, 1]), P.poly([3, 1]))
        self.assertEqual(P.gcd(a, b), P.poly([-1, 1]))  # 首一 x - 1

    def test_gcd_coprime(self):
        self.assertEqual(P.gcd(P.poly([1, 0, 1]), P.poly([0, 1])), P.poly([1]))

    def test_squarefree_factorization_repeated(self):
        # (x-1)^3 (x+2)^2 (x-3)
        p = P.poly([-1, 1])
        p = P.mul(P.mul(p, P.poly([-1, 1])), P.poly([-1, 1]))
        p = P.mul(p, P.mul(P.poly([2, 1]), P.poly([2, 1])))
        p = P.mul(p, P.poly([-3, 1]))
        lead, facs = P.squarefree_factorization(p)
        self.assertEqual(lead, 1)
        self.assertEqual(
            {m: P.fmt_poly(f) for f, m in facs},
            {1: "x - 3", 2: "x + 2", 3: "x - 1"})

    def test_squarefree_part_removes_multiplicity(self):
        p = P.mul(P.poly([-1, 1]), P.poly([-1, 1]))  # (x-1)^2
        self.assertEqual(P.squarefree_part(p), P.poly([-1, 1]))

    def test_isolate_rational_exact(self):
        roots = P.isolate_real_roots(P.poly([4, -2]))  # -2x+4
        self.assertEqual(roots, [Fraction(2)])

    def test_isolate_irrational_intervals(self):
        roots = P.isolate_real_roots(P.poly([-2, 0, 1]))  # x^2-2
        self.assertEqual(len(roots), 2)
        for item in roots:
            self.assertIsInstance(item, tuple)
            lo, hi = item
            self.assertLess(lo, hi)
            self.assertLessEqual(hi - lo, Fraction(1, 10**6))
        # 区间确实夹住 ±sqrt(2)：比较平方（负侧 |lo| > sqrt2 > |hi|）
        neg_lo, neg_hi = roots[0]
        self.assertGreater(neg_lo**2, 2)
        self.assertLess(neg_hi**2, 2)
        pos_lo, pos_hi = roots[1]
        self.assertLess(pos_lo**2, 2)
        self.assertGreater(pos_hi**2, 2)

    def test_isolate_repeated_root_once(self):
        p = P.mul(P.mul(P.poly([-1, 1]), P.poly([-1, 1])), P.poly([-2, 1]))
        self.assertEqual(P.isolate_real_roots(p), [Fraction(1), Fraction(2)])

    def test_isolate_no_real_roots(self):
        self.assertEqual(P.isolate_real_roots(P.poly([1, 0, 1])), [])

    def test_sturm_count(self):
        self.assertEqual(P.count_real_roots(P.poly([-2, 0, 1]), 0, 3), 1)
        self.assertEqual(P.count_real_roots(P.poly([-2, 0, 1]), -3, 3), 2)


class TestRationalFunction(unittest.TestCase):
    def test_x_over_x(self):
        f = rf([0, 1], [0, 1])
        self.assertEqual(f.num, P.poly([1]))
        self.assertEqual(f.den, P.poly([1]))
        self.assertEqual(f.exclusions, [Fraction(0)])
        self.assertEqual(f.evaluate(5), 1)
        with self.assertRaises(DomainError):
            f.evaluate(0)

    def test_zero_denominator_rejected(self):
        with self.assertRaises(ValueError):
            rf([1], [])
        with self.assertRaises(ValueError):
            rf([], [])  # 0/0 同样拒绝

    def test_zero_function_keeps_exclusions(self):
        f = rf([], [-1, 1])  # 0/(x-1)
        self.assertEqual(f.num, P.poly([]))
        self.assertEqual(f.den, P.poly([1]))
        self.assertEqual(f.exclusions, [Fraction(1)])
        self.assertEqual(f.evaluate(5), 0)
        with self.assertRaises(DomainError):
            f.evaluate(1)

    def test_repeated_root_cancellation(self):
        xm1 = P.poly([-1, 1])
        num = P.mul(P.mul(xm1, xm1), P.poly([2, 1]))   # (x-1)^2 (x+2)
        den = P.mul(P.mul(xm1, xm1), xm1)              # (x-1)^3
        f = rf(num, den)
        self.assertEqual(f.num, P.poly([2, 1]))
        self.assertEqual(f.den, P.poly([-1, 1]))
        self.assertEqual(f.exclusions, [Fraction(1)])  # 可去奇点保留
        with self.assertRaises(DomainError):
            f.evaluate(1)
        self.assertEqual(f.evaluate(0), -2)

    def test_irrational_exclusions(self):
        x2m2 = P.poly([-2, 0, 1])
        f = rf(P.mul(x2m2, P.poly([-1, 1])), x2m2)  # (x^2-2)(x-1)/(x^2-2)
        self.assertEqual(f.num, P.poly([-1, 1]))
        self.assertEqual(f.den, P.poly([1]))
        self.assertEqual(len(f.exclusions), 2)
        self.assertTrue(all(isinstance(e, tuple) for e in f.exclusions))
        # 区间内的有理点被排除；区间外的点正常求值
        lo, hi = f.exclusions[1]
        mid = (lo + hi) / 2
        self.assertTrue(is_excluded(f.exclusions, mid))
        with self.assertRaises(DomainError):
            f.evaluate(mid)
        self.assertEqual(f.evaluate(Fraction(3, 2)), Fraction(1, 2))

    def test_denominator_leading_sign_normalized(self):
        f = rf([1], [4, -2])  # 1/(-2x+4)
        self.assertEqual(f.den, P.poly([-2, 1]))   # x - 2 首一
        self.assertEqual(f.num, P.poly([Fraction(-1, 2)]))
        self.assertEqual(f.exclusions, [Fraction(2)])
        self.assertEqual(f.evaluate(0), Fraction(1, 4))

    def test_addition_merges_exclusions(self):
        f = rf([1], [-1, 1]) + rf([1], [-2, 1])
        self.assertEqual(f.num, P.poly([-3, 2]))
        self.assertEqual(f.den, P.poly([2, -3, 1]))
        self.assertEqual(f.exclusions, [Fraction(1), Fraction(2)])
        self.assertEqual(f.evaluate(0), Fraction(-3, 2))
        for x in (1, 2):
            with self.assertRaises(DomainError):
                f.evaluate(x)

    def test_multiplication_keeps_removable_exclusions(self):
        h = rf([-1, 1], [-2, 1])   # (x-1)/(x-2)
        k = rf([-2, 1], [-3, 1])   # (x-2)/(x-3)
        f = h * k
        self.assertEqual(f.num, P.poly([-1, 1]))
        self.assertEqual(f.den, P.poly([-3, 1]))
        self.assertEqual(f.exclusions, [Fraction(2), Fraction(3)])
        with self.assertRaises(DomainError):
            f.evaluate(2)
        self.assertEqual(f.evaluate(0), Fraction(1, 3))

    def test_division_adds_numerator_roots(self):
        f = rf([1], [-1, 1]) / rf([-2, 1], [1])  # 1/(x-1) / (x-2)
        self.assertEqual(f.exclusions, [Fraction(1), Fraction(2)])
        with self.assertRaises(ValueError):
            rf([1], [1]) / rf([], [1])  # 除以零函数

    def test_values_agree_before_after(self):
        num = P.mul(P.poly([-2, 0, 1]), P.poly([-1, 1]))
        f = rf(num, P.poly([-2, 0, 1]))
        for x in (Fraction(0), Fraction(3, 2), Fraction(-7, 5), Fraction(10)):
            self.assertEqual(f.evaluate(x), f.evaluate_original(x))

    def test_history_states_domain(self):
        f = rf([0, 1], [0, 1])
        self.assertTrue(any("适用域" in h for h in f.history))
        self.assertGreaterEqual(len(f.history), 2)


class TestVerify(unittest.TestCase):
    CASES = [
        ([0, 1], [0, 1]),                                  # x/x
        ([], [-1, 1]),                                     # 零函数
        ([-2, 0, 1], [-2, 0, 1]),                          # 整体约去
        ([2, -3, 1], [-1, 0, 0, 1]),                       # (x-1)(x-2)/(x^3-x)
        ([1], [4, -2]),                                    # 负首项分母
    ]

    def test_identity(self):
        for num, den in self.CASES:
            with self.subTest(num=num, den=den):
                self.assertTrue(verify_identity(rf(num, den)))

    def test_exclusions_legal(self):
        for num, den in self.CASES:
            with self.subTest(num=num, den=den):
                self.assertTrue(verify_exclusions(rf(num, den)))

    def test_sampling(self):
        for num, den in self.CASES:
            with self.subTest(num=num, den=den):
                self.assertEqual(sample_verify(rf(num, den), count=16), 16)

    def test_sampling_detects_tampering(self):
        f = rf([0, 1], [0, 1])
        f.num = P.poly([2])  # 篡改化简结果
        self.assertFalse(verify_identity(f))
        with self.assertRaises(AssertionError):
            sample_verify(f, count=4)


class TestExclusionSet(unittest.TestCase):
    def test_normalize_merges_overlaps(self):
        items = [(Fraction(0), Fraction(2)), (Fraction(1), Fraction(3)),
                 Fraction(5), Fraction(5), Fraction(1)]
        # 点 1 落在合并区间 (0,3) 内被丢弃，点 5 去重
        self.assertEqual(normalize_exclusions(items),
                         [(Fraction(0), Fraction(3)), Fraction(5)])

    def test_membership(self):
        excl = [Fraction(2), (Fraction(3), Fraction(4))]
        self.assertTrue(is_excluded(excl, 2))
        self.assertTrue(is_excluded(excl, Fraction(7, 2)))
        self.assertFalse(is_excluded(excl, 3))      # 开区间端点不排除
        self.assertFalse(is_excluded(excl, 5))


if __name__ == "__main__":
    unittest.main()
