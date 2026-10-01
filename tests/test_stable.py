import math
import unittest

from amplification import stable


class TestBoundaries(unittest.TestCase):
    def test_q_zero_gives_exact_zero(self):
        for eps in (0.0, 1e-8, 1.0, 700.0, 5000.0):
            self.assertEqual(stable.eps_prime(0.0, eps), 0.0)

    def test_q_one_gives_original_eps_exactly(self):
        for eps in (1e-12, 1e-8, 0.1, 1.0, 10.0, 700.0, 5000.0):
            self.assertEqual(stable.eps_prime(1.0, eps), eps)

    def test_eps_zero_gives_exact_zero(self):
        for q in (0.0, 1e-12, 0.5, 1.0):
            self.assertEqual(stable.eps_prime(q, 0.0), 0.0)


class TestStableEvaluation(unittest.TestCase):
    def test_moderate_values_match_direct_formula(self):
        # Direct formula is safe for moderate eps; cross-check the stable path.
        for q, eps in ((0.1, 1.0), (0.5, 2.0), (0.01, 0.5), (0.9, 5.0)):
            direct = math.log1p(q * math.expm1(eps))
            got = stable.eps_prime(q, eps)
            self.assertTrue(math.isclose(got, direct, rel_tol=1e-14),
                            f"q={q}, eps={eps}: {got!r} vs {direct!r}")

    def test_tiny_q(self):
        q, eps = 1e-12, 1.0
        got = stable.eps_prime(q, eps)
        # eps' ~ q*(e^eps - 1) for tiny q
        self.assertTrue(math.isclose(got, q * math.expm1(eps), rel_tol=1e-12))
        self.assertGreater(got, 0.0)

    def test_tiny_eps(self):
        q, eps = 0.5, 1e-8
        got = stable.eps_prime(q, eps)
        self.assertAlmostEqual(got, math.log1p(q * math.expm1(eps)), places=18)

    def test_huge_eps_no_overflow(self):
        # exp(1000) overflows float64; the stable form must not.
        for eps in (700.0, 709.0, 710.0, 1000.0, 5000.0):
            q = 1e-3
            got = stable.eps_prime(q, eps)
            self.assertTrue(math.isfinite(got))
            # For huge eps, eps' = eps + log(q) + o(1).
            self.assertAlmostEqual(got, eps + math.log(q), places=6)

    def test_huge_eps_tiny_q_moderate_result(self):
        # q*(e^eps - 1) is moderate even though e^eps alone overflows.
        q, eps = 1e-300, 800.0
        got = stable.eps_prime(q, eps)
        self.assertTrue(math.isfinite(got))
        self.assertAlmostEqual(got, eps + math.log(q), places=5)

    def test_result_below_float_resolution_gives_zero(self):
        # True eps' ~ 1e-320 * 1e-8 underflows; 0.0 is the correct float.
        self.assertEqual(stable.eps_prime(1e-320, 1e-8), 0.0)

    def test_monotone_in_both_arguments(self):
        qs = [1e-6, 1e-3, 0.1, 0.5, 0.9, 1.0]
        epss = [1e-6, 0.01, 1.0, 10.0, 100.0]
        vals = [[stable.eps_prime(q, e) for e in epss] for q in qs]
        for row in vals:
            self.assertTrue(all(a <= b for a, b in zip(row, row[1:])))
        for j in range(len(epss)):
            col = [vals[i][j] for i in range(len(qs))]
            self.assertTrue(all(a <= b for a, b in zip(col, col[1:])))


class TestRejection(unittest.TestCase):
    def test_rejects_out_of_domain_inputs(self):
        bad = [(-0.1, 1.0), (1.1, 1.0), (0.5, -1.0),
               (float("nan"), 1.0), (0.5, float("nan")),
               (float("inf"), 1.0), (0.5, float("inf"))]
        for q, eps in bad:
            with self.assertRaises(ValueError, msg=f"q={q}, eps={eps}"):
                stable.eps_prime(q, eps)


if __name__ == "__main__":
    unittest.main()
