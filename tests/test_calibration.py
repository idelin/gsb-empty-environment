import itertools
import random
import unittest

from permtest import (
    ExchangeabilityError,
    clopper_pearson,
    grouped_calibration,
    ks_statistic,
    paired_calibration,
)


class TestGroupedExact(unittest.TestCase):
    def test_hand_verified_p_value(self):
        # Pool [1,2,3,4], n_a = 2, KS of the observed split {1,2}|{3,4} is 1.
        # Of the C(4,2)=6 splits exactly two reach KS = 1, so p = 2/6.
        result = grouped_calibration([1, 2], [3, 4], "ks")
        self.assertTrue(result.exact)
        self.assertEqual(result.method, "exact")
        self.assertAlmostEqual(result.observed, 1.0)
        self.assertAlmostEqual(result.p_value, 2 / 6)
        self.assertEqual(result.num_permutations, 6)
        self.assertEqual(result.exceedances, 2)
        self.assertEqual(result.sizes, (2, 2))
        self.assertIsNone(result.mc_interval)

    def test_full_permutation_reference(self):
        # The combination-based exact p-value must agree with the reference
        # distribution over ALL permutations of the group labels.
        a, b = [1, 2, 5], [3, 4, 6]
        result = grouped_calibration(a, b, "ks")
        pool = a + b
        labels = [0] * len(a) + [1] * len(b)
        exceed = 0
        total = 0
        for perm in itertools.permutations(labels):
            pa = [v for v, lab in zip(pool, perm) if lab == 0]
            pb = [v for v, lab in zip(pool, perm) if lab == 1]
            if ks_statistic(pa, pb) >= result.observed - 1e-12:
                exceed += 1
            total += 1
        self.assertAlmostEqual(result.p_value, exceed / total)

    def test_sharded_matches_unsharded(self):
        a, b = [5, 1, 9, 3], [2, 8, 4, 7, 6]
        base = grouped_calibration(a, b, "ks", num_shards=1)
        for num_shards in [2, 3, 8, 1000]:
            sharded = grouped_calibration(a, b, "ks", num_shards=num_shards)
            self.assertEqual(base.p_value, sharded.p_value)
            self.assertEqual(base.num_permutations, sharded.num_permutations)

    def test_label_reorder_invariance(self):
        a, b = [3, 1, 4, 1, 5], [9, 2, 6, 5]
        rng = random.Random(7)
        expected = grouped_calibration(a, b, "tv")
        for _ in range(5):
            a2, b2 = a[:], b[:]
            rng.shuffle(a2)
            rng.shuffle(b2)
            result = grouped_calibration(a2, b2, "tv")
            self.assertEqual(expected.observed, result.observed)
            self.assertEqual(expected.p_value, result.p_value)

    def test_custom_callable(self):
        def range_diff(xs, ys):
            return abs((max(xs) - min(xs)) - (max(ys) - min(ys)))

        result = grouped_calibration([1, 2, 3], [4, 5, 6], range_diff)
        self.assertEqual(result.statistic, "range_diff")
        self.assertTrue(result.exact)

    def test_unknown_statistic_rejected(self):
        with self.assertRaises(ValueError):
            grouped_calibration([1], [2], "nope")


class TestGroupedMonteCarlo(unittest.TestCase):
    def test_monte_carlo_labels_and_interval(self):
        # comb(24, 12) > default exact threshold -> Monte Carlo path.
        a = list(range(12))
        b = list(range(12, 24))
        result = grouped_calibration(a, b, "ks", seed=42, num_mc=2000)
        self.assertFalse(result.exact)
        self.assertEqual(result.method, "monte_carlo")
        self.assertEqual(result.seed, 42)
        self.assertEqual(result.sizes, (12, 12))
        self.assertEqual(result.num_permutations, 2000)
        lo, hi = result.mc_interval
        self.assertTrue(0.0 <= lo <= hi <= 1.0)
        # Observed KS = 1 is the maximum possible; p-hat must be tiny.
        self.assertLess(result.p_value, 0.05)

    def test_injected_rng_reproducible(self):
        a, b = list(range(12)), list(range(12, 24))
        r1 = grouped_calibration(a, b, "ks", rng=random.Random(123), num_mc=500)
        r2 = grouped_calibration(a, b, "ks", rng=random.Random(123), num_mc=500)
        self.assertEqual(r1.p_value, r2.p_value)
        self.assertEqual(r1.exceedances, r2.exceedances)

    def test_monte_carlo_tracks_exact(self):
        # Force the MC path on a small design and check the exact p-value
        # falls inside the reported Clopper-Pearson interval.
        a, b = [1, 2, 5, 6], [3, 4, 7, 8]
        exact = grouped_calibration(a, b, "ks")
        mc = grouped_calibration(a, b, "ks", exact_threshold=0, num_mc=5000, seed=2024)
        self.assertFalse(mc.exact)
        lo, hi = mc.mc_interval
        self.assertLessEqual(lo, exact.p_value)
        self.assertGreaterEqual(hi, exact.p_value)

    def test_label_reorder_invariance_mc(self):
        a, b = list(range(12)), list(range(5, 17))
        rng = random.Random(3)
        a2, b2 = a[:], b[:]
        rng.shuffle(a2)
        rng.shuffle(b2)
        r1 = grouped_calibration(a, b, "ks", seed=11, num_mc=500)
        r2 = grouped_calibration(a2, b2, "ks", seed=11, num_mc=500)
        self.assertEqual(r1.observed, r2.observed)
        self.assertEqual(r1.p_value, r2.p_value)


class TestPaired(unittest.TestCase):
    def test_hand_verified_p_value(self):
        # Pairs (1,2),(2,1): observed |mean diff| = 0; all 4 sign flips
        # give statistic >= 0, so p = 1.
        result = paired_calibration([1, 2], [2, 1], "mean_diff")
        self.assertTrue(result.exact)
        self.assertAlmostEqual(result.observed, 0.0)
        self.assertAlmostEqual(result.p_value, 1.0)
        self.assertEqual(result.num_permutations, 4)
        self.assertEqual(result.sizes, (2,))

    def test_hand_verified_extreme(self):
        # Pairs (1,5),(2,6),(3,7): observed mean diff = 4. Only the
        # identity and full-flip masks reach 4, so p = 2/8.
        result = paired_calibration([1, 2, 3], [5, 6, 7], "mean_diff")
        self.assertAlmostEqual(result.observed, 4.0)
        self.assertAlmostEqual(result.p_value, 2 / 8)

    def test_sharded_matches_unsharded(self):
        x = [1, 2, 3, 4, 5, 6, 7]
        y = [2, 1, 4, 3, 6, 5, 8]
        base = paired_calibration(x, y, "mean_diff", num_shards=1)
        sharded = paired_calibration(x, y, "mean_diff", num_shards=5)
        self.assertEqual(base.p_value, sharded.p_value)
        self.assertEqual(base.num_permutations, sharded.num_permutations)

    def test_pair_order_invariance(self):
        x = [1, 2, 3, 4]
        y = [4, 1, 3, 2]
        order = [2, 0, 3, 1]
        x2 = [x[i] for i in order]
        y2 = [y[i] for i in order]
        r1 = paired_calibration(x, y, "mean_diff")
        r2 = paired_calibration(x2, y2, "mean_diff")
        self.assertEqual(r1.observed, r2.observed)
        self.assertEqual(r1.p_value, r2.p_value)

    def test_monte_carlo_path(self):
        x = list(range(25))
        y = [v + 1 for v in x]
        result = paired_calibration(x, y, "mean_diff", seed=5, num_mc=1000)
        self.assertFalse(result.exact)
        self.assertEqual(result.method, "monte_carlo")
        self.assertEqual(result.seed, 5)
        lo, hi = result.mc_interval
        self.assertTrue(0.0 <= lo <= hi <= 1.0)
        self.assertLess(result.p_value, 0.05)

    def test_length_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            paired_calibration([1, 2], [1])


class TestExchangeabilityRefusal(unittest.TestCase):
    def test_grouped_refused(self):
        with self.assertRaises(ExchangeabilityError):
            grouped_calibration([1, 2], [3, 4], exchangeable=False)

    def test_paired_refused(self):
        with self.assertRaises(ExchangeabilityError):
            paired_calibration([1, 2], [3, 4], exchangeable=False)


class TestEmptyRejected(unittest.TestCase):
    def test_grouped(self):
        with self.assertRaises(ValueError):
            grouped_calibration([], [1, 2])
        with self.assertRaises(ValueError):
            grouped_calibration([1, 2], [])

    def test_paired(self):
        with self.assertRaises(ValueError):
            paired_calibration([], [])


class TestClopperPearson(unittest.TestCase):
    def test_known_values(self):
        # Classic reference: 3 successes in 10 trials, 95% interval
        # approximately (0.0667, 0.6525).
        lo, hi = clopper_pearson(3, 10, 0.05)
        self.assertAlmostEqual(lo, 0.0667, places=3)
        self.assertAlmostEqual(hi, 0.6525, places=3)

    def test_edge_cases(self):
        lo, hi = clopper_pearson(0, 10)
        self.assertEqual(lo, 0.0)
        self.assertLess(hi, 0.5)
        lo, hi = clopper_pearson(10, 10)
        self.assertEqual(hi, 1.0)
        self.assertGreater(lo, 0.5)

    def test_monotone_in_x(self):
        prev = (-1.0, -1.0)
        for x in range(0, 21):
            lo, hi = clopper_pearson(x, 20)
            self.assertGreater(lo, prev[0])
            self.assertGreater(hi, prev[1])
            prev = (lo, hi)


if __name__ == "__main__":
    unittest.main()
