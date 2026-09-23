import unittest
from fractions import Fraction

from rational_power_series import (
    NonZeroConstantError,
    TruncatedPowerSeries,
    TruncationError,
    ZeroConstantError,
    add,
    compose,
    inverse,
    multiply,
)


class TruncatedPowerSeriesTest(unittest.TestCase):
    def test_coefficients_are_exact_fractions_and_order_pads(self):
        series = TruncatedPowerSeries([1, Fraction(1, 2), Fraction(1, 3)], order=5)

        self.assertEqual(series.order, 5)
        self.assertEqual(
            series.coefficients,
            (
                Fraction(1),
                Fraction(1, 2),
                Fraction(1, 3),
                Fraction(0),
                Fraction(0),
            ),
        )
        self.assertTrue(all(isinstance(value, Fraction) for value in series))

    def test_float_coefficients_are_rejected(self):
        with self.assertRaises(TypeError):
            TruncatedPowerSeries([1.0])

    def test_addition_propagates_smaller_order(self):
        left = TruncatedPowerSeries([1, 2, 3, 4], order=5)
        right = TruncatedPowerSeries([10, 20, 30], order=3)

        result = add(left, right)

        self.assertEqual(result.order, 3)
        self.assertEqual(result.coefficients, (Fraction(11), Fraction(22), Fraction(33)))

    def test_multiplication_keeps_only_requested_order(self):
        left = TruncatedPowerSeries([1, 2], order=4)
        right = TruncatedPowerSeries([3, 4], order=4)

        result = multiply(left, right, order=2)

        self.assertEqual(result.order, 2)
        self.assertEqual(result.coefficients, (Fraction(3), Fraction(10)))

    def test_multiplication_computes_exact_rational_convolution(self):
        left = TruncatedPowerSeries(
            [Fraction(1, 2), Fraction(2, 3), Fraction(-1, 4)], order=4
        )
        right = TruncatedPowerSeries(
            [Fraction(3, 5), Fraction(-2), Fraction(7, 2)], order=4
        )

        result = multiply(left, right, order=4)

        self.assertEqual(
            result.coefficients,
            (
                Fraction(3, 10),
                Fraction(-3, 5),
                Fraction(4, 15),
                Fraction(17, 6),
            ),
        )

    def test_multiplication_rejects_order_outside_known_domain(self):
        left = TruncatedPowerSeries([1, 1], order=2)
        right = TruncatedPowerSeries([1, 1], order=3)

        with self.assertRaises(TruncationError):
            multiply(left, right, order=3)

    def test_multiplication_can_return_order_zero(self):
        result = multiply(
            TruncatedPowerSeries.one(2),
            TruncatedPowerSeries.one(2),
            order=0,
        )

        self.assertEqual(result, TruncatedPowerSeries.zero(0))

    def test_inverse_satisfies_product_equal_to_one_in_truncation_range(self):
        series = TruncatedPowerSeries(
            [2, Fraction(3, 2), -4, Fraction(1, 7)], order=6
        )
        reciprocal = inverse(series, order=6)

        for order in range(1, 7):
            self.assertEqual(
                multiply(series, reciprocal, order),
                TruncatedPowerSeries.one(order),
                msg=f"inverse identity failed modulo x^{order}",
            )

    def test_inverse_uses_input_order_by_default(self):
        series = TruncatedPowerSeries([2, 3], order=4)
        reciprocal = inverse(series)

        self.assertEqual(reciprocal.order, 4)
        self.assertEqual(
            reciprocal.coefficients,
            (
                Fraction(1, 2),
                Fraction(-3, 4),
                Fraction(9, 8),
                Fraction(-27, 16),
            ),
        )

    def test_zero_constant_inverse_is_an_error_not_a_zero_result(self):
        series = TruncatedPowerSeries([0, 1, 2], order=3)

        with self.assertRaises(ZeroConstantError):
            inverse(series)

    def test_inverse_requires_positive_and_available_order(self):
        series = TruncatedPowerSeries([2, 1], order=3)

        with self.assertRaises(TruncationError):
            inverse(series, order=0)
        with self.assertRaises(TruncationError):
            inverse(series, order=4)

    def test_composition_with_zero_inner_constant(self):
        outer = TruncatedPowerSeries([1, 2, 1], order=4)
        inner = TruncatedPowerSeries([0, 1, 3], order=4)

        result = compose(outer, inner)

        self.assertEqual(result.order, 4)
        self.assertEqual(result.coefficients, (1, 2, 7, 6))

    def test_composition_default_order_is_smaller_input_order(self):
        outer = TruncatedPowerSeries([1, 1], order=5)
        inner = TruncatedPowerSeries([0, 1], order=3)

        result = compose(outer, inner)

        self.assertEqual(result.order, 3)
        self.assertEqual(result.coefficients, (1, 1, 0))

    def test_composition_accepts_explicit_valid_order(self):
        outer = TruncatedPowerSeries([7, 2, 3, 4], order=4)
        inner = TruncatedPowerSeries([0, 1, 1], order=4)

        result = compose(outer, inner, order=2)

        self.assertEqual(result.order, 2)
        self.assertEqual(result.coefficients, (7, 2))

    def test_composition_rejects_nonzero_inner_constant(self):
        outer = TruncatedPowerSeries([1, 2, 3], order=3)
        inner = TruncatedPowerSeries([1, 1], order=3)

        with self.assertRaises(NonZeroConstantError):
            compose(outer, inner)

    def test_composition_rejects_order_outside_known_domain(self):
        outer = TruncatedPowerSeries([1, 2], order=2)
        inner = TruncatedPowerSeries([0, 1], order=3)

        with self.assertRaises(TruncationError):
            compose(outer, inner, order=3)


if __name__ == "__main__":
    unittest.main(verbosity=2)
