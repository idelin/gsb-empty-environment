import random
import unittest

from filterverify.fir_bounds import derivative_bound, fir_band_bounds, fir_response

FS = 8000.0


class FirBoundTest(unittest.TestCase):
    def test_bounds_bracket_dense_sampling(self):
        rng = random.Random(42)
        coeffs = [rng.uniform(-0.2, 0.2) for _ in range(31)]
        f_lo, f_hi = 500.0, 3000.0
        b = fir_band_bounds(coeffs, FS, f_lo, f_hi, grid_points=41)
        # dense sampling to approximate the true extrema
        dense = [abs(fir_response(coeffs, f_lo + i * (f_hi - f_lo) / 4000.0, FS))
                 for i in range(4001)]
        self.assertGreaterEqual(b.upper_bound, max(dense))
        self.assertLessEqual(b.lower_bound, min(dense) + 1e-12)
        self.assertGreater(b.between_grid_margin, 0.0)

    def test_margin_shrinks_with_finer_grid(self):
        coeffs = [0.1, -0.2, 0.5, -0.2, 0.1]
        coarse = fir_band_bounds(coeffs, FS, 0.0, 3900.0, grid_points=21)
        fine = fir_band_bounds(coeffs, FS, 0.0, 3900.0, grid_points=201)
        self.assertLess(fine.between_grid_margin, coarse.between_grid_margin)

    def test_derivative_bound(self):
        # demodulated bound: tau = 1.0, sum |n-tau|*|h_n| = 0.5 + 0.125
        coeffs = [0.5, -0.25, 0.125]
        d = derivative_bound(coeffs, FS)
        import math
        self.assertAlmostEqual(d, 2 * math.pi / FS * (1 * 0.5 + 0 * 0.25 + 1 * 0.125))


if __name__ == "__main__":
    unittest.main()
