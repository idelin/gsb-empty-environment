import unittest
from fractions import Fraction

from psd_gaussian import exact_check_psd, exact_verify_decomposition
from psd_gaussian.exact import quadratic_form_exact, to_fraction_matrix


class TestExactRational(unittest.TestCase):
    def test_exact_ldl_relation_holds(self):
        cov = [[2, 1, 0], [1, 2, 1], [0, 1, 2]]
        report = exact_check_psd(cov)
        # Exact conclusion, not an estimate.
        self.assertEqual(report.kind, "exact")
        self.assertTrue(report.is_psd)
        self.assertIsNone(report.witness)
        self.assertTrue(exact_verify_decomposition(report.L, report.D, cov))

    def test_exact_singular_psd(self):
        cov = [[1, 1], [1, 1]]  # rank 1, PSD
        report = exact_check_psd(cov)
        self.assertTrue(report.is_psd)
        self.assertEqual(report.D, [Fraction(1), Fraction(0)])
        self.assertTrue(exact_verify_decomposition(report.L, report.D, cov))

    def test_exact_witness_for_indefinite(self):
        cov = [[1, 2], [2, 1]]
        report = exact_check_psd(cov)
        self.assertFalse(report.is_psd)
        m = to_fraction_matrix(cov)
        # The witness certificate is exact rational arithmetic.
        self.assertEqual(
            quadratic_form_exact(report.witness, m), report.witness_value
        )
        self.assertLess(report.witness_value, 0)

    def test_zero_pivot_with_nonzero_row_is_indefinite(self):
        cov = [[0, 1], [1, 1]]
        report = exact_check_psd(cov)
        self.assertFalse(report.is_psd)
        self.assertLess(report.witness_value, 0)

    def test_rational_entries(self):
        cov = [
            [Fraction(1, 2), Fraction(1, 4)],
            [Fraction(1, 4), Fraction(1, 3)],
        ]
        report = exact_check_psd(cov)
        self.assertTrue(report.is_psd)
        self.assertTrue(exact_verify_decomposition(report.L, report.D, cov))

    def test_non_symmetric_rejected(self):
        with self.assertRaises(ValueError):
            exact_check_psd([[1, 2], [3, 1]])


if __name__ == "__main__":
    unittest.main()
