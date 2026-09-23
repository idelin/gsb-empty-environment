"""unittest suite for rational.py.

The identity checks in here are deliberately INDEPENDENT of the
simplifier's gcd code:

* "algebraic identity" checks expand the cross-product
  p1*q2 == p2*q1 purely by convolution + Horner evaluation;
* "rational sampling" checks evaluate both sides at many exact rationals
  outside the exclusion set and compare Fraction values.
"""

import unittest
from fractions import Fraction

from rational import (
    ExclusionPoint,
    IsolatedRoot,
    Polynomial,
    RationalFunction,
    RationalPoint,
    ZeroDenominatorError,
)

X = Polynomial([0, 1])


# ---------------------------------------------------------------------------
# Independent verification primitives (do not use Polynomial.gcd)
# ---------------------------------------------------------------------------


def convolve(a, b):
    out = [Fraction(0)] * (len(a) + len(b) - 1)
    for i, ci in enumerate(a):
        for j, cj in enumerate(b):
            out[i + j] += ci * cj
    return out


def horner(coeffs, x):
    total = Fraction(0)
    for c in reversed(coeffs):
        total = total * x + c
    return total


def assert_fractions_equal(testcase, f, num_coeffs, den_coeffs):
    """Cross-product identity p*q_expected == p_expected*q."""
    p, q = f.numerator.coeffs, f.denominator.coeffs
    expected_num = tuple(Fraction(c) for c in num_coeffs)
    expected_den = tuple(Fraction(c) for c in den_coeffs)
    testcase.assertEqual(
        convolve(p, expected_den), convolve(expected_num, q)
    )


def sample_values(func, points):
    for r in points:
        r = Fraction(r)
        if func.exclusion_poly(r) == 0:
            continue
        yield r, func(r)


def assert_same_values(testcase, f, g, samples):
    for r in samples:
        r = Fraction(r)
        if f.exclusion_poly(r) == 0 or g.exclusion_poly(r) == 0:
            continue
        testcase.assertEqual(
            f(r),
            g(r),
            msg=f"values differ at x={r}: {f(r)} != {g(r)}",
        )


def rational_sample_grid(limit=12):
    grid = []
    for n in range(-limit, limit + 1):
        for d in (1, 2, 3, 5, 7):
            grid.append(Fraction(n, d))
    return grid


class PolynomialTests(unittest.TestCase):
    def test_arithmetic_exact(self):
        self.assertEqual((X + 1) * (X - 1), X ** 2 - 1)
        q, r = divmod(X ** 3 - 1, X - 1)
        self.assertEqual(r, Polynomial([0]))
        self.assertEqual(q, X ** 2 + X + 1)
        self.assertEqual((Polynomial([1, 1]) ** 3), Polynomial([1, 3, 3, 1]))

    def test_gcd_repeated_roots(self):
        p = (X - 1) ** 2 * (X - 2)
        q = (X - 1) ** 3
        self.assertEqual(Polynomial.gcd(p, q), (X - 1) ** 2)

    def test_square_free_yun(self):
        f = (X - 1) ** 2 * (X - 2) ** 3
        factors = f.square_free()
        self.assertEqual(
            sorted(m for m, _ in factors), [2, 3]
        )
        by_mult = {m: poly for m, poly in factors}
        self.assertEqual(by_mult[2], X - 1)
        self.assertEqual(by_mult[3], X - 2)
        # Product reconstruction (up to leading coefficient = 1).
        product = Polynomial([1])
        for mult, poly in factors:
            product = product * poly ** mult
        self.assertEqual(product, f.monic())

    def test_square_free_simple_polynomial(self):
        self.assertEqual(
            [poly for _, poly in (X ** 2 - 2).square_free()], [X ** 2 - 2]
        )

    def test_rational_roots(self):
        self.assertEqual(
            Polynomial([0, 2, 1, -5, 2]).rational_roots(),
            sorted(map(Fraction, [0, 1, 2, Fraction(-1, 2)])),
        )

    def test_root_isolation_irrational(self):
        roots = (X ** 3 - 3 * X + 1).isolate_real_roots()
        self.assertEqual(len(roots), 3)
        self.assertTrue(all(isinstance(r, IsolatedRoot) for r in roots))
        intervals = [(float(r.lo), float(r.hi)) for r in roots]
        for (l1, h1), (l2, h2) in zip(intervals, intervals[1:]):
            self.assertLessEqual(h1, l2)  # disjoint, ordered
        for r in roots:
            self.assertLessEqual(r.hi - r.lo, Fraction(1, 10**6))
            # Sign straddling across the isolating interval.
            self.assertLessEqual(
                r.descriptor(r.lo) * r.descriptor(r.hi), Fraction(0)
            )

    def test_root_isolation_counts(self):
        cases = {
            X ** 2 - 2: 2,
            X ** 2 + 1: 0,
            X ** 3 - X: 3,
            (X ** 2 - 2) * (X ** 2 - 3): 4,
            (X - 1) ** 2: 1,
        }
        for poly, count in cases.items():
            with self.subTest(poly=str(poly)):
                self.assertEqual(len(poly.isolate_real_roots()), count)


class RationalFunctionTests(unittest.TestCase):
    GRID = rational_sample_grid()

    def test_x_over_x(self):
        f = RationalFunction(X, X)
        self.assertEqual(f.numerator, Polynomial([1]))
        self.assertEqual(f.denominator, Polynomial([1]))
        self.assertEqual(str(f), "1")
        exclusions = f.exclusions
        self.assertEqual(exclusions, [RationalPoint(0)])
        self.assertEqual(f(Fraction(7)), 1)
        with self.assertRaises(ZeroDivisionError):
            f(Fraction(0))
        assert_fractions_equal(self, f, [1], [1])

    def test_zero_denominator_rejected(self):
        with self.assertRaises(ZeroDenominatorError):
            RationalFunction(X, Polynomial([0]))
        with self.assertRaises(ZeroDenominatorError):
            RationalFunction(Polynomial([1]), Polynomial([0]))

    def test_values_unchanged_by_reduction(self):
        original = RationalFunction(X * (X - 1) * (X + 1), X * (X - 1))
        reduced = RationalFunction(X + 1, Polynomial([1]))
        assert_fractions_equal(self, original, [1, 1], [1])
        assert_same_values(self, original, reduced, self.GRID)
        # Removed singularities remain excluded.
        excluded = {p for p in original.exclusions if p.is_rational()}
        self.assertEqual(
            excluded, {RationalPoint(0), RationalPoint(1)}
        )

    def test_repeated_root_cancel(self):
        p = (X - 2) ** 3
        f = RationalFunction(p * (X + 5), p)
        assert_fractions_equal(self, f, [5, 1], [1])
        self.assertEqual(f.exclusions, [RationalPoint(2)])
        assert_same_values(
            self,
            f,
            RationalFunction(X + 5, Polynomial([1])),
            self.GRID,
        )

    def test_common_factor_with_irrational_roots(self):
        p = X ** 2 - 2
        f = RationalFunction(p, p)
        self.assertEqual(str(f), "1")
        exclusions = f.exclusions
        self.assertEqual(len(exclusions), 2)
        self.assertTrue(all(isinstance(e, IsolatedRoot) for e in exclusions))
        assert_fractions_equal(self, f, [1], [1])
        # Original p/p and constant 1 agree at every admissible rational.
        assert_same_values(
            self, f, RationalFunction(Polynomial([1])), self.GRID
        )

    def test_zero_function(self):
        f = RationalFunction(Polynomial([0]), X - 3)
        self.assertTrue(f.is_zero_function)
        self.assertEqual(f.numerator, Polynomial([0]))
        self.assertEqual(f.denominator, Polynomial([1]))
        self.assertEqual(f.exclusions, [RationalPoint(3)])
        for r, value in sample_values(f, self.GRID):
            self.assertEqual(value, 0)
        with self.assertRaises(ZeroDivisionError):
            f(Fraction(3))

    def test_zero_function_zero_denominator(self):
        with self.assertRaises(ZeroDenominatorError):
            RationalFunction(Polynomial([0]), Polynomial([0]))

    def test_denominator_leading_sign(self):
        f = RationalFunction(X, -X)
        self.assertEqual(f.denominator.lc, 1)
        self.assertEqual(str(f), "-1")
        self.assertEqual(f.exclusions, [RationalPoint(0)])
        g = RationalFunction(Polynomial([1]), Polynomial([-2, 0, 1]))
        self.assertGreater(g.denominator.lc, 0)

    def test_addition_keeps_all_domains(self):
        a = RationalFunction(X, X)                       # 1, exclude 0
        b = RationalFunction(Polynomial([1]), X ** 2 - 1)  # exclude ±1
        result = a + b
        assert_fractions_equal(self, result, [0, 0, 1], [-1, 0, 1])
        excluded = {
            e.value for e in result.exclusions if e.is_rational()
        }
        self.assertEqual(excluded, {Fraction(-1), Fraction(0), Fraction(1)})
        assert_same_values(
            self,
            result,
            RationalFunction(X ** 2, X ** 2 - 1),
            self.GRID,
        )

    def test_multiplication_division_domain_union(self):
        a = RationalFunction(X, X - 1)           # exclude 1
        b = RationalFunction(X - 1, X)           # exclude 0
        product = a * b
        self.assertEqual(product.numerator, Polynomial([1]))
        excluded = {
            e.value for e in product.exclusions if e.is_rational()
        }
        self.assertEqual(excluded, {Fraction(0), Fraction(1)})
        assert_same_values(
            self,
            product,
            RationalFunction(Polynomial([1])),
            self.GRID,
        )
        quotient = a / b
        # (x/(x-1)) / ((x-1)/x) = x^2/(x-1)^2; exclude 1 (both) and 0.
        assert_fractions_equal(self, quotient, [0, 0, 1], [1, -2, 1])
        q_excluded = {
            e.value for e in quotient.exclusions if e.is_rational()
        }
        self.assertEqual(q_excluded, {Fraction(0), Fraction(1)})
        assert_same_values(
            self,
            quotient,
            RationalFunction(X ** 2, (X - 1) ** 2),
            self.GRID,
        )

    def test_division_by_zero_function_rejected(self):
        a = RationalFunction(X, X - 1)
        zero = RationalFunction(Polynomial([0]), X - 4)
        with self.assertRaises(ZeroDenominatorError):
            a / zero

    def test_chained_combinations_update_exclusions(self):
        a = RationalFunction(X, X)                 # exclude 0
        b = RationalFunction(Polynomial([1]), X - 1)  # exclude 1
        c = RationalFunction(Polynomial([1]), X - 2)  # exclude 2
        result = (a + b) * c
        excluded = {
            e.value for e in result.exclusions if e.is_rational()
        }
        self.assertEqual(excluded, {Fraction(0), Fraction(1), Fraction(2)})
        # Compare against independently built raw combination.
        # a+b = 1 + 1/(x-1) = x/(x-1) on domain excluding 0 and 1.
        raw_num = X
        raw_den = (X - 1) * (X - 2)
        assert_same_values(
            self,
            result,
            RationalFunction(raw_num, raw_den),
            self.GRID,
        )

    def test_irrational_combined_exclusions(self):
        p = X ** 2 - 2
        f = RationalFunction(X * p, p)             # = x, exclude ±sqrt 2
        self.assertEqual(str(f), "x")
        self.assertEqual(len(f.exclusions), 2)
        g = RationalFunction(Polynomial([1]), X)   # exclude 0
        total = f + g
        exclusions = total.exclusions
        rational_pts = [e for e in exclusions if e.is_rational()]
        irrational_pts = [e for e in exclusions if not e.is_rational()]
        self.assertEqual(rational_pts, [RationalPoint(0)])
        self.assertEqual(len(irrational_pts), 2)
        assert_same_values(
            self, total, RationalFunction(X ** 2 + 1, X), self.GRID
        )

    def test_steps_describe_domains(self):
        f = (RationalFunction(X, X) +
             RationalFunction(Polynomial([1]), X - 1))
        joined = "\n".join(f.steps)
        self.assertIn("domain", joined)
        self.assertIn("gcd", joined)
        explain = f.explain()
        self.assertIn("Domain:", explain)

    def test_evaluation_against_horner_independently(self):
        f = RationalFunction((X - 1) * (X + 2), (X - 1) * (X - 3))
        for r in self.GRID:
            r = Fraction(r)
            if r in (1, 3):
                continue
            raw = horner(list((X + 2).coeffs), r) / horner(
                list((X - 3).coeffs), r
            )
            self.assertEqual(f(r), raw)


if __name__ == "__main__":
    unittest.main(verbosity=2)
