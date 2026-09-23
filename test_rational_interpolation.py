import unittest
from fractions import Fraction

from rational_interpolation import (
    ConflictingObservations,
    HermiteStructureError,
    RationalHermiteInterpolator,
    as_fraction,
    format_newton,
    format_power,
    solve_constraints,
)


class RationalInterpolationTests(unittest.TestCase):
    def test_incremental_newton_and_power_forms(self):
        interpolator = RationalHermiteInterpolator()
        interpolator.add_point(0, 0)
        interpolator.add_point(1, 1)
        self.assertEqual(interpolator.to_power(), (Fraction(0), Fraction(1)))

        interpolator.add_point(2, 4)
        nodes, coefficients = interpolator.to_newton()
        self.assertEqual(nodes, (Fraction(0), Fraction(1), Fraction(2)))
        self.assertEqual(coefficients, (Fraction(0), Fraction(1), Fraction(1)))
        self.assertEqual(interpolator.to_power(), (Fraction(0), Fraction(0), Fraction(1)))
        self.assertTrue(interpolator.verify().passed)
        self.assertGreater(interpolator.last_build_stats["hits"], 0)

    def test_hermite_repeated_nodes_and_derivatives(self):
        interpolator = RationalHermiteInterpolator()
        interpolator.add_point(0, 0)
        interpolator.add_derivative(0, 0)
        interpolator.add_point(1, 1)
        interpolator.add_derivative(1, 3)

        self.assertEqual(
            interpolator.to_power(),
            (Fraction(0), Fraction(0), Fraction(0), Fraction(1)),
        )
        self.assertEqual(interpolator.evaluate(Fraction(1, 2)), Fraction(1, 8))
        self.assertEqual(interpolator.derivative(1), Fraction(3))
        report = interpolator.verify()
        self.assertTrue(report.passed)
        self.assertTrue(report.all_values_match)
        self.assertTrue(report.all_derivatives_match)

    def test_duplicate_same_value_is_idempotent_and_conflict_is_diagnosed(self):
        interpolator = RationalHermiteInterpolator()
        first = interpolator.add_point("1/2", "2/3")
        second = interpolator.add_point(Fraction(1, 2), Fraction(2, 3))
        self.assertEqual(first, second)
        self.assertEqual(interpolator.observation_count, 1)

        with self.assertRaises(ConflictingObservations) as caught:
            interpolator.add_point(Fraction(1, 2), Fraction(3, 4))
        self.assertEqual(caught.exception.residual, Fraction(-1, 12))
        self.assertIn(f"existing#{first.id}", caught.exception.witness)
        self.assertEqual(interpolator.observation_count, 1)
        self.assertEqual(interpolator.evaluate(Fraction(1, 2)), Fraction(2, 3))

    def test_remove_middle_observation_rebuilds_not_truncates(self):
        interpolator = RationalHermiteInterpolator()
        interpolator.add_point(0, 0)
        interpolator.add_point(1, 3)
        interpolator.add_point(3, 12)
        interpolator.add_derivative(0, 1)
        self.assertEqual(
            interpolator.to_power(),
            (Fraction(0), Fraction(1), Fraction(5, 2), Fraction(-1, 2)),
        )

        interpolator.remove_point(1)
        self.assertEqual(
            interpolator.to_power(),
            (Fraction(0), Fraction(1), Fraction(1)),
        )
        self.assertEqual(interpolator.evaluate(3), Fraction(12))
        self.assertEqual(interpolator.derivative(1), Fraction(3))
        self.assertTrue(interpolator.verify().passed)
        self.assertGreater(interpolator.last_build_stats["hits"], 0)

    def test_remove_derivative_and_readd_it_incrementally(self):
        interpolator = RationalHermiteInterpolator()
        interpolator.add_point(0, 0)
        interpolator.add_derivative(0, 0)
        interpolator.add_point(1, 1)
        interpolator.add_derivative(1, 3)
        self.assertEqual(interpolator.to_power(), (0, 0, 0, 1))

        interpolator.remove_derivative(0)
        self.assertEqual(interpolator.to_power(), (0, -1, 2))
        interpolator.add_derivative(0, 0)
        self.assertEqual(interpolator.to_power(), (0, 0, 0, 1))
        self.assertTrue(interpolator.verify().passed)

    def test_node_order_permutation_is_mathematically_equivalent(self):
        interpolator = RationalHermiteInterpolator()
        interpolator.add_point(0, 0)
        interpolator.add_point(1, 2)
        interpolator.add_derivative(1, 3)
        interpolator.add_point(3, 12)
        before = interpolator.to_power()

        interpolator.reorder_nodes([3, 0, 1])
        nodes, _ = interpolator.to_newton()
        self.assertEqual(nodes, (3, 0, 1, 1))
        self.assertEqual(interpolator.to_power(), before)
        self.assertTrue(interpolator.verify().passed)

        reversed_inserter = RationalHermiteInterpolator()
        reversed_inserter.add_point(3, 12)
        reversed_inserter.add_derivative(1, 3)
        reversed_inserter.add_point(1, 2)
        reversed_inserter.add_point(0, 0)
        self.assertEqual(reversed_inserter.to_power(), before)

    def test_independent_solver_solves_and_certifies_inconsistency(self):
        solved = solve_constraints(
            [("value", 0, 0), ("value", 1, 1), ("value", 2, 4)]
        )
        self.assertEqual(solved.status, "unique")
        self.assertEqual(solved.coefficients, (0, 0, 1))

        inconsistent = solve_constraints(
            [("value", 0, 1), ("value", 0, 2)]
        )
        self.assertEqual(inconsistent.status, "inconsistent")
        self.assertEqual(inconsistent.witness, {0: 1, 1: -1})
        self.assertEqual(inconsistent.residual, -1)

        underdetermined = solve_constraints(
            [("value", 0, 1), ("value", 0, 1)]
        )
        self.assertEqual(underdetermined.status, "underdetermined")
        self.assertEqual(underdetermined.rank, 1)

    def test_derivative_only_data_is_diagnosed_by_linear_reference(self):
        interpolator = RationalHermiteInterpolator()
        interpolator.add_point(0, 0)
        interpolator.add_derivative(0, 1)
        interpolator.add_point(1, 2)
        interpolator.remove_point(0)

        with self.assertRaises(HermiteStructureError):
            interpolator.to_power()
        diagnosis = interpolator.diagnose()
        self.assertEqual(diagnosis["status"], "unique")
        self.assertEqual(diagnosis["coefficients"], (1, 1))
        self.assertIsNotNone(diagnosis["newton_domain_error"])

        interpolator.remove_derivative(0)
        self.assertEqual(interpolator.to_power(), (2,))

    def test_rational_bit_growth_is_recorded(self):
        interpolator = RationalHermiteInterpolator()
        interpolator.add_point(0, 0)
        interpolator.add_point(Fraction(1, 2), Fraction(1, 3))
        interpolator.add_point(Fraction(3, 2), Fraction(5, 7))

        history = interpolator.bit_growth_history
        self.assertEqual([entry["observations"] for entry in history], [0, 1, 2, 3])
        final = history[-1]
        self.assertEqual(final["status"], "ok")
        self.assertGreaterEqual(
            final["power_denominator_bits"], final["input_denominator_bits"]
        )
        self.assertIn("newton_numerator_bits", final)
        self.assertTrue(interpolator.verify().passed)

    def test_exact_input_contract_and_formatting(self):
        with self.assertRaises(TypeError):
            as_fraction(0.1)

        interpolator = RationalHermiteInterpolator()
        interpolator.add_point(0, 0)
        interpolator.add_point(1, 1)
        interpolator.add_derivative(1, 3)
        nodes, coefficients = interpolator.to_newton()
        self.assertEqual(format_power(interpolator.to_power()), "-x + 2*x^2")
        self.assertEqual(format_newton(nodes, coefficients), "x + 2*x*(x - 1)")


if __name__ == "__main__":
    unittest.main()
