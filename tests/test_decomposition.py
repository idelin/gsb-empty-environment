import math
import unittest

from psd_gaussian import (
    NotPositiveSemidefiniteError,
    NotSymmetricError,
    PSDTolerance,
    decompose,
)
from psd_gaussian import linalg


class TestDecomposition(unittest.TestCase):
    def test_known_psd_residual_is_tiny(self):
        cov = [[4.0, 1.0, 1.0], [1.0, 3.0, 0.0], [1.0, 0.0, 2.0]]
        dec = decompose(cov)
        # Exact (deterministic) statement about the factorization.
        self.assertLess(dec.residual_fro, 1e-10)
        self.assertLess(dec.residual_max, 1e-10)
        self.assertEqual(dec.rank, 3)
        self.assertEqual(dec.truncated_eigenvalues, [])

    def test_reconstruct_matches_input(self):
        cov = [[2.0, 0.5], [0.5, 1.0]]
        dec = decompose(cov)
        recon = dec.reconstruct()
        for i in range(2):
            for j in range(2):
                self.assertAlmostEqual(recon[i][j], cov[i][j], places=10)

    def test_non_psd_rejected_with_witness(self):
        cov = [[1.0, 2.0], [2.0, 1.0]]  # eigenvalues 3 and -1
        with self.assertRaises(NotPositiveSemidefiniteError) as ctx:
            decompose(cov)
        err = ctx.exception
        self.assertLess(err.eigenvalue, 0.0)
        # The witness direction certifies the failure: v^T Sigma v < 0.
        self.assertLess(linalg.quadratic_form(err.witness, cov), 0.0)
        self.assertAlmostEqual(linalg.norm2(err.witness), 1.0, places=10)

    def test_no_square_root_of_negative_eigenvalues(self):
        # Rejection must happen before any sqrt; no NaN factor is produced.
        with self.assertRaises(NotPositiveSemidefiniteError):
            decompose([[1.0, 0.0], [0.0, -0.5]])
        cov = [[1.0, 0.0], [0.0, 2.0]]
        dec = decompose(cov)
        for row in dec.factor:
            for x in row:
                self.assertTrue(math.isfinite(x))

    def test_near_singular_truncated_not_jittered(self):
        # Eigenvalue -5e-13 lies inside the default tolerance band: it is
        # truncated to zero and *reported*, never hidden by added jitter.
        cov = [[1.0, 0.0], [0.0, -5e-13]]
        dec = decompose(cov)
        self.assertEqual(len(dec.truncated_eigenvalues), 1)
        self.assertAlmostEqual(dec.truncated_eigenvalues[0], -5e-13, places=20)
        self.assertLess(dec.residual_fro, 1e-10)

    def test_near_singular_beyond_tolerance_rejected(self):
        with self.assertRaises(NotPositiveSemidefiniteError):
            decompose([[1.0, 0.0], [0.0, -1e-3]])

    def test_configurable_tolerance(self):
        cov = [[1.0, 0.0], [0.0, -1e-4]]
        with self.assertRaises(NotPositiveSemidefiniteError):
            decompose(cov, PSDTolerance(atol=1e-6, rtol=1e-6))
        dec = decompose(cov, PSDTolerance(atol=1e-3, rtol=1e-3))
        self.assertEqual(len(dec.truncated_eigenvalues), 1)

    def test_rank1_factor(self):
        u = [1.0, 2.0, 3.0]
        cov = [[x * y for y in u] for x in u]
        dec = decompose(cov)
        self.assertEqual(dec.rank, 1)
        self.assertLess(dec.residual_fro, 1e-10)

    def test_zero_matrix_is_psd(self):
        dec = decompose([[0.0, 0.0], [0.0, 0.0]])
        self.assertEqual(dec.rank, 0)
        self.assertEqual(dec.residual_fro, 0.0)

    def test_non_symmetric_rejected(self):
        with self.assertRaises(NotSymmetricError):
            decompose([[1.0, 0.5], [0.1, 1.0]])


if __name__ == "__main__":
    unittest.main()
