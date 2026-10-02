import itertools
import random
import sys
import unittest
from fractions import Fraction
from math import comb
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from ecdfcal import (
    ECDF,
    Design,
    Statistic,
    calibrate,
    clopper_pearson_interval,
    frequency_table,
    ks_statistic,
    rank_combination,
    shard_ranges,
    tv_distance,
    unrank_combination,
)


class TestECDF(unittest.TestCase):
    def test_ties_jump_simultaneously_by_full_multiplicity(self):
        ecdf = ECDF.from_samples([1, 2, 2, 3])
        self.assertEqual(ecdf.support, (1, 2, 3))
        self.assertEqual(ecdf.multiplicities, (1, 2, 1))
        # single simultaneous jump of mass 1/2 at value 2
        self.assertEqual(ecdf.cdf_at(1), Fraction(1, 4))
        self.assertEqual(ecdf.cdf_at(Fraction(3, 2)), Fraction(1, 4))
        self.assertEqual(ecdf.cdf_at(2), Fraction(3, 4))
        self.assertEqual(ecdf.cdf_at(3), Fraction(1))

    def test_weights_and_identity_are_explicit(self):
        ecdf = ECDF.from_samples([1, 1, 2], weights=[1, 2, 1])
        self.assertEqual(ecdf.multiplicities, (2, 1))       # identities
        self.assertEqual(ecdf.masses, (Fraction(3, 4), Fraction(1, 4)))
        self.assertEqual(ecdf.n_observations, 3)
        self.assertEqual(ecdf.total_weight, 4)

    def test_weight_validation(self):
        with self.assertRaises(ValueError):
            ECDF.from_samples([1, 2], weights=[1])
        with self.assertRaises(ValueError):
            ECDF.from_samples([1, 2], weights=[1, 0])

    def test_empty_sample_rejected(self):
        with self.assertRaises(ValueError):
            ECDF.from_samples([])
        with self.assertRaises(ValueError):
            calibrate([], [1, 2], design=Design.UNPAIRED_EXCHANGEABLE)
        with self.assertRaises(ValueError):
            calibrate([1], [], design=Design.UNPAIRED_EXCHANGEABLE)
        with self.assertRaises(ValueError):
            calibrate([], [], design=Design.PAIRED_EXCHANGEABLE)

    def test_input_order_irrelevant(self):
        self.assertEqual(
            ECDF.from_samples([3, 1, 2, 2]), ECDF.from_samples([2, 2, 1, 3])
        )


class TestStatistics(unittest.TestCase):
    def test_frequency_table(self):
        table = frequency_table(ECDF.from_samples([1, 2, 2, 3]))
        self.assertEqual(
            table,
            [
                (1, 1, Fraction(1, 4), Fraction(1, 4)),
                (2, 2, Fraction(1, 2), Fraction(3, 4)),
                (3, 1, Fraction(1, 4), Fraction(1)),
            ],
        )

    def test_ks_known_value(self):
        a = ECDF.from_samples([1, 2, 3])
        b = ECDF.from_samples([2, 3, 4])
        # |F_a - F_b| = 1/3 at each of 1, 2, 3 and 0 at 4
        self.assertEqual(ks_statistic(a, b), Fraction(1, 3))

    def test_ks_with_ties_uses_simultaneous_jumps(self):
        a = ECDF.from_samples([1, 1, 1])
        b = ECDF.from_samples([1, 2])
        # at 1: |1 - 1/2| = 1/2 ; at 2: |1 - 1| = 0
        self.assertEqual(ks_statistic(a, b), Fraction(1, 2))

    def test_tv_known_value(self):
        a = ECDF.from_samples([1, 2])
        b = ECDF.from_samples([2, 3])
        # 1/2 * (|1/2-0| + |1/2-1/2| + |0-1/2|) = 1/2
        self.assertEqual(tv_distance(a, b), Fraction(1, 2))

    def test_tv_identical_samples_is_zero(self):
        a = ECDF.from_samples([1, 1, 2])
        b = ECDF.from_samples([2, 1, 1])
        self.assertEqual(tv_distance(a, b), 0)
        self.assertEqual(ks_statistic(a, b), 0)


class TestCombinatorics(unittest.TestCase):
    def test_unrank_rank_roundtrip_exhaustive(self):
        n, k = 8, 3
        seen = set()
        for rank in range(comb(n, k)):
            combo = unrank_combination(rank, n, k)
            self.assertEqual(rank_combination(combo, n), rank)
            seen.add(combo)
        self.assertEqual(seen, set(itertools.combinations(range(n), k)))

    def test_unrank_out_of_range(self):
        with self.assertRaises(ValueError):
            unrank_combination(comb(5, 2), 5, 2)

    def test_shard_ranges_cover_exactly_once(self):
        for total, n_shards in [(97, 8), (10, 3), (5, 5), (3, 10), (1, 4)]:
            ranges = shard_ranges(total, n_shards)
            covered = [r for lo, hi in ranges for r in range(lo, hi)]
            self.assertEqual(sorted(covered), list(range(total)))
            self.assertEqual(len(covered), len(set(covered)))  # no duplicates

    def test_shard_ranges_validation(self):
        with self.assertRaises(ValueError):
            shard_ranges(0, 2)
        with self.assertRaises(ValueError):
            shard_ranges(10, 0)


def _brute_force_unpaired_exceedances(pooled, n_a, stat_fn, observed):
    n = len(pooled)
    count = 0
    for idx in itertools.combinations(range(n), n_a):
        chosen = set(idx)
        fa = ECDF.from_samples(pooled[i] for i in idx)
        fb = ECDF.from_samples(pooled[i] for i in range(n) if i not in chosen)
        if stat_fn(fa, fb) >= observed:
            count += 1
    return count


class TestUnpairedCalibration(unittest.TestCase):
    def setUp(self):
        self.a = [1, 2, 2, 5]
        self.b = [3, 4, 4, 6]

    def test_exact_matches_brute_force(self):
        for stat in (Statistic.KS, Statistic.TV):
            result = calibrate(self.a, self.b,
                               design=Design.UNPAIRED_EXCHANGEABLE,
                               statistic=stat, method="exact")
            pooled = sorted(self.a) + sorted(self.b)
            observed = (ks_statistic if stat is Statistic.KS else tv_distance)(
                ECDF.from_samples(self.a), ECDF.from_samples(self.b))
            expected = _brute_force_unpaired_exceedances(
                pooled, len(self.a),
                ks_statistic if stat is Statistic.KS else tv_distance, observed)
            self.assertEqual(result.kind, "exact")
            self.assertEqual(result.exceedances, expected)
            self.assertEqual(result.reference_size, comb(8, 4))
            self.assertEqual(result.p_value, Fraction(expected, comb(8, 4)))
            self.assertGreaterEqual(result.exceedances, 1)  # identity assignment

    def test_sharded_parallel_equals_serial(self):
        serial = calibrate(self.a, self.b,
                           design=Design.UNPAIRED_EXCHANGEABLE,
                           method="exact", n_shards=1)
        sharded = calibrate(self.a, self.b,
                            design=Design.UNPAIRED_EXCHANGEABLE,
                            method="exact", n_shards=4)
        parallel = calibrate(self.a, self.b,
                             design=Design.UNPAIRED_EXCHANGEABLE,
                             method="exact", n_shards=4, parallel=True)
        self.assertEqual(serial, sharded)
        self.assertEqual(serial, parallel)

    def test_label_reordering_does_not_change_result(self):
        base = calibrate(self.a, self.b,
                         design=Design.UNPAIRED_EXCHANGEABLE, method="exact")
        shuffled = calibrate([5, 1, 2, 2], [4, 6, 3, 4],
                             design=Design.UNPAIRED_EXCHANGEABLE, method="exact")
        self.assertEqual(base, shuffled)

    def test_monte_carlo_records_seed_and_size(self):
        result = calibrate(self.a, self.b,
                           design=Design.UNPAIRED_EXCHANGEABLE,
                           method="monte_carlo", n_permutations=500, seed=42)
        self.assertEqual(result.kind, "monte_carlo")
        self.assertEqual(result.seed, 42)
        self.assertEqual(result.reference_size, 500)
        self.assertEqual(result.p_value,
                         Fraction(result.exceedances + 1, 501))
        self.assertIsNotNone(result.ci_low)
        self.assertLessEqual(result.ci_low, result.ci_high)

    def test_monte_carlo_reproducible_and_order_invariant(self):
        r1 = calibrate(self.a, self.b, design=Design.UNPAIRED_EXCHANGEABLE,
                       method="monte_carlo", n_permutations=300, seed=7)
        r2 = calibrate(self.a, self.b, design=Design.UNPAIRED_EXCHANGEABLE,
                       method="monte_carlo", n_permutations=300, seed=7)
        r3 = calibrate(list(reversed(self.a)), list(reversed(self.b)),
                       design=Design.UNPAIRED_EXCHANGEABLE,
                       method="monte_carlo", n_permutations=300, seed=7)
        self.assertEqual(r1, r2)
        self.assertEqual(r1, r3)

    def test_monte_carlo_interval_covers_exact_value(self):
        exact = calibrate(self.a, self.b,
                          design=Design.UNPAIRED_EXCHANGEABLE, method="exact")
        mc = calibrate(self.a, self.b, design=Design.UNPAIRED_EXCHANGEABLE,
                       method="monte_carlo", n_permutations=2000, seed=2024)
        self.assertLessEqual(mc.ci_low, float(exact.p_value))
        self.assertLessEqual(float(exact.p_value), mc.ci_high)

    def test_monte_carlo_requires_injected_randomness(self):
        with self.assertRaises(ValueError):
            calibrate(self.a, self.b, design=Design.UNPAIRED_EXCHANGEABLE,
                      method="monte_carlo")

    def test_caller_supplied_rng_is_used(self):
        result = calibrate(self.a, self.b,
                           design=Design.UNPAIRED_EXCHANGEABLE,
                           method="monte_carlo", n_permutations=100,
                           rng=random.Random(9), seed=9)
        self.assertEqual(result.seed, 9)
        self.assertEqual(result.kind, "monte_carlo")


class TestPairedCalibration(unittest.TestCase):
    def setUp(self):
        self.before = [5, 3, 4, 2, 6]
        self.after = [7, 3, 6, 2, 9]

    def test_exact_matches_brute_force_sign_flips(self):
        result = calibrate(self.before, self.after,
                           design=Design.PAIRED_EXCHANGEABLE, method="exact")
        pairs = sorted(zip(self.before, self.after))
        observed = ks_statistic(
            ECDF.from_samples(x for x, _ in pairs),
            ECDF.from_samples(y for _, y in pairs))
        count = 0
        for signs in itertools.product((0, 1), repeat=len(pairs)):
            sa = [pairs[i][signs[i]] for i in range(len(pairs))]
            sb = [pairs[i][1 - signs[i]] for i in range(len(pairs))]
            if ks_statistic(ECDF.from_samples(sa), ECDF.from_samples(sb)) >= observed:
                count += 1
        self.assertEqual(result.kind, "exact")
        self.assertEqual(result.exceedances, count)
        self.assertEqual(result.reference_size, 2 ** len(pairs))
        self.assertEqual(result.p_value, Fraction(count, 2 ** len(pairs)))

    def test_pair_order_reordering_invariant(self):
        base = calibrate(self.before, self.after,
                         design=Design.PAIRED_EXCHANGEABLE, method="exact")
        order = [2, 0, 4, 1, 3]
        shuffled = calibrate([self.before[i] for i in order],
                             [self.after[i] for i in order],
                             design=Design.PAIRED_EXCHANGEABLE, method="exact")
        self.assertEqual(base, shuffled)

    def test_sharded_parallel_equals_serial(self):
        serial = calibrate(self.before, self.after,
                           design=Design.PAIRED_EXCHANGEABLE, method="exact")
        parallel = calibrate(self.before, self.after,
                             design=Design.PAIRED_EXCHANGEABLE,
                             method="exact", n_shards=3, parallel=True)
        self.assertEqual(serial, parallel)

    def test_paired_requires_equal_lengths(self):
        with self.assertRaises(ValueError):
            calibrate([1, 2, 3], [1, 2], design=Design.PAIRED_EXCHANGEABLE)

    def test_paired_monte_carlo(self):
        result = calibrate(self.before, self.after,
                           design=Design.PAIRED_EXCHANGEABLE,
                           method="monte_carlo", n_permutations=400, seed=5)
        self.assertEqual(result.kind, "monte_carlo")
        self.assertEqual(result.seed, 5)
        exact = calibrate(self.before, self.after,
                          design=Design.PAIRED_EXCHANGEABLE, method="exact")
        self.assertLessEqual(result.ci_low, float(exact.p_value))
        self.assertLessEqual(float(exact.p_value), result.ci_high)


class TestRefusal(unittest.TestCase):
    def test_not_exchangeable_refuses_p_value(self):
        result = calibrate([1, 2, 3], [4, 5, 6],
                           design=Design.NOT_EXCHANGEABLE)
        self.assertTrue(result.refused)
        self.assertIsNone(result.p_value)
        self.assertIsNone(result.kind)
        self.assertIsNotNone(result.refusal_reason)
        # observed statistic is still reported
        self.assertEqual(result.observed, Fraction(1))

    def test_exact_and_mc_are_labelled_separately(self):
        exact = calibrate([1, 2], [3, 4],
                          design=Design.UNPAIRED_EXCHANGEABLE, method="exact")
        mc = calibrate([1, 2], [3, 4],
                       design=Design.UNPAIRED_EXCHANGEABLE,
                       method="monte_carlo", n_permutations=100, seed=1)
        self.assertEqual(exact.kind, "exact")
        self.assertIsNone(exact.ci_low)
        self.assertEqual(mc.kind, "monte_carlo")
        self.assertIsNotNone(mc.ci_low)


class TestClopperPearson(unittest.TestCase):
    def test_boundaries(self):
        self.assertEqual(clopper_pearson_interval(0, 10)[0], 0.0)
        self.assertEqual(clopper_pearson_interval(10, 10)[1], 1.0)

    def test_known_value(self):
        # CP 95% interval for 3/10 is approx (0.0667, 0.6525)
        low, high = clopper_pearson_interval(3, 10)
        self.assertAlmostEqual(low, 0.0667, places=3)
        self.assertAlmostEqual(high, 0.6525, places=3)

    def test_monotone_in_x(self):
        lows = [clopper_pearson_interval(x, 100)[0] for x in range(0, 101, 10)]
        self.assertEqual(lows, sorted(lows))


if __name__ == "__main__":
    unittest.main()
