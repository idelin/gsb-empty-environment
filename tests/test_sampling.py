import random
import unittest

from psd_gaussian import audit_samples, decompose, sample_correlated


def make_cov():
    return [[1.0, 0.5], [0.5, 2.0]]


class TestSampling(unittest.TestCase):
    def test_same_seed_reproduces_samples(self):
        dec = decompose(make_cov())
        s1, _ = sample_correlated(dec, [0.0, 0.0], 50, random.Random(7), seed=7)
        s2, _ = sample_correlated(dec, [0.0, 0.0], 50, random.Random(7), seed=7)
        self.assertEqual(s1, s2)

    def test_stream_recovery_does_not_change_samples(self):
        # Restoring an RNG stream snapshot must reproduce the tail exactly.
        dec = decompose(make_cov())
        rng = random.Random(123)
        head, _ = sample_correlated(dec, [0.0, 0.0], 10, rng, seed=123)
        state = rng.getstate()
        tail1, _ = sample_correlated(dec, [0.0, 0.0], 20, rng, seed=123)
        rng.setstate(state)
        tail2, _ = sample_correlated(dec, [0.0, 0.0], 20, rng, seed=123)
        self.assertEqual(tail1, tail2)
        self.assertNotEqual(head, tail1[:10])

    def test_record_contains_seed_and_size(self):
        dec = decompose(make_cov())
        _, record = sample_correlated(dec, [0.0, 0.0], 40, random.Random(9), seed=9)
        self.assertEqual(record.seed, 9)
        self.assertEqual(record.n_samples, 40)
        self.assertEqual(record.dim, 2)
        self.assertEqual(record.kind, "empirical")

    def test_rank1_samples_stay_in_subspace(self):
        u = [1.0, 2.0, 3.0]
        cov = [[x * y for y in u] for x in u]
        dec = decompose(cov)
        mean = [5.0, -1.0, 0.5]
        samples, _ = sample_correlated(dec, mean, 200, random.Random(3), seed=3)
        # Two independent directions orthogonal to u.
        w1 = [2.0, -1.0, 0.0]
        w2 = [3.0, 0.0, -1.0]
        for s in samples:
            d = [s[i] - mean[i] for i in range(3)]
            self.assertAlmostEqual(sum(a * b for a, b in zip(d, w1)), 0.0, places=7)
            self.assertAlmostEqual(sum(a * b for a, b in zip(d, w2)), 0.0, places=7)

    def test_zero_variance_dimension_is_constant(self):
        cov = [[1.0, 0.0, 0.0], [0.0, 0.0, 0.0], [0.0, 0.0, 4.0]]
        dec = decompose(cov)
        mean = [1.0, 2.0, 3.0]
        samples, _ = sample_correlated(dec, mean, 100, random.Random(5), seed=5)
        for s in samples:
            self.assertEqual(s[1], mean[1])  # exactly constant, no jitter

    def test_empirical_covariance_audit_is_close(self):
        # Empirical statement: accuracy depends on seed and sample size.
        cov = make_cov()
        mean = [0.0, 0.0]
        dec = decompose(cov)
        samples, record = sample_correlated(
            dec, mean, 20000, random.Random(42), seed=42
        )
        audit = audit_samples(samples, mean, cov, record)
        self.assertEqual(audit.kind, "empirical")
        self.assertEqual(audit.seed, 42)
        self.assertEqual(audit.n_samples, 20000)
        self.assertLess(audit.cov_error_relative, 0.05)
        self.assertLess(audit.mean_error_max, 0.05)


if __name__ == "__main__":
    unittest.main()
