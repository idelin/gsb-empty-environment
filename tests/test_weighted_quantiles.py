import random
import unittest
from fractions import Fraction

from weighted_quantiles import (EXACT, NaiveWeightedReference, WeightedStore,
                                merge_shards, run_random_transactions,
                                split_shards)


def make_store(**obs):
    store = WeightedStore(random.Random(0))
    for obs_id, (value, weight) in obs.items():
        store.upsert(obs_id, value, weight)
    return store


class QuantileDefinitionTests(unittest.TestCase):
    def test_basic_weighted_quantiles(self):
        store = make_store(a=(1, 1), b=(2, 1), c=(3, 2))
        # total weight 4; cumulative: F(1)=1/4, F(2)=1/2, F(3)=1
        self.assertEqual(store.quantile(0).low, 1)
        self.assertEqual(store.quantile(1).low, 3)
        r = store.quantile(Fraction(1, 4))
        # q == F(1) exactly: jump boundary -> interval [1, 2]
        self.assertTrue(r.is_interval)
        self.assertEqual((r.low, r.high), (1, 2))
        r = store.quantile(Fraction(2, 5))  # strictly inside the jump at 2
        self.assertFalse(r.is_interval)
        self.assertEqual(r.low, 2)
        r = store.quantile(Fraction(3, 4))
        self.assertEqual(r.low, 3)
        self.assertEqual(r.label, EXACT)

    def test_jump_boundary_returns_interval(self):
        store = make_store(a=(1, 1), b=(2, 1), c=(3, 2))
        # q = 1/2 == F(2) exactly: any x in [2, 3] is a valid median
        r = store.quantile(Fraction(1, 2))
        self.assertTrue(r.is_interval)
        self.assertEqual((r.low, r.high), (2, 3))

    def test_q0_q1_are_min_max_positive_weight_values(self):
        store = make_store(a=(5, 1), b=(9, 2), c=(7, 1))
        self.assertEqual(store.quantile(0).low, 5)
        self.assertEqual(store.quantile(1).low, 9)

    def test_q_out_of_range_rejected(self):
        store = make_store(a=(1, 1))
        for bad in (-1, Fraction(3, 2)):
            with self.assertRaises(ValueError):
                store.quantile(bad)

    def test_empty_store_raises(self):
        store = WeightedStore(random.Random(0))
        with self.assertRaises(ValueError):
            store.quantile(Fraction(1, 2))
        with self.assertRaises(ValueError):
            store.cdf(0)

    def test_cdf(self):
        store = make_store(a=(1, 1), b=(2, 1), c=(3, 2))
        self.assertEqual(store.cdf(0), Fraction(0))
        self.assertEqual(store.cdf(2), Fraction(1, 2))
        self.assertEqual(store.cdf(3), Fraction(1))
        self.assertEqual(store.cdf(100), Fraction(1))

    def test_exact_rational_weights(self):
        store = make_store(a=(1, Fraction(1, 3)), b=(2, Fraction(1, 3)),
                           c=(3, Fraction(1, 3)))
        self.assertEqual(store.total_weight(), 1)
        # q = 1/3 hits the boundary F(1) exactly -> interval [1, 2]
        r = store.quantile(Fraction(1, 3))
        self.assertEqual((r.low, r.high), (1, 2))


class WeightPolicyTests(unittest.TestCase):
    def test_negative_weight_rejected(self):
        store = WeightedStore(random.Random(0))
        with self.assertRaises(ValueError):
            store.upsert("a", 1, Fraction(-1, 2))
        with self.assertRaises(ValueError):
            store.upsert("a", 1, -0.5)

    def test_zero_weight_eliminated(self):
        store = make_store(a=(1, 1))
        store.upsert("b", 2, 0)
        self.assertEqual(len(store), 1)
        self.assertEqual(store.total_weight(), 1)
        # updating an existing observation to zero weight retracts it
        store.upsert("a", 1, 0)
        self.assertEqual(len(store), 0)
        self.assertEqual(store.total_weight(), 0)

    def test_duplicate_values_merged(self):
        store = make_store(a=(5, 1), b=(5, 2), c=(5, 3))
        self.assertEqual(list(store.items()), [(5, Fraction(6))])
        self.assertEqual(store.total_weight(), 6)


class UpdateSemanticsTests(unittest.TestCase):
    def test_idempotent_upsert(self):
        store = WeightedStore(random.Random(0))
        self.assertTrue(store.upsert("a", 1, 2, subject="s", timestamp=10))
        self.assertFalse(store.upsert("a", 1, 2, subject="s", timestamp=10))
        self.assertEqual(store.total_weight(), 2)
        self.assertEqual(len(store), 1)

    def test_update_replaces_payload(self):
        store = WeightedStore(random.Random(0))
        store.upsert("a", 1, 2, subject="s1")
        store.upsert("a", 5, 3, subject="s2")
        self.assertEqual(store.total_weight(), 3)
        self.assertEqual(list(store.items()), [(5, Fraction(3))])
        # old subject no longer owns anything
        self.assertEqual(store.retract_subject("s1"), 0)
        self.assertEqual(store.retract_subject("s2"), 1)
        self.assertEqual(len(store), 0)

    def test_retract_keeps_total_weight_consistent(self):
        store = make_store(a=(1, 2), b=(2, 3), c=(3, 5))
        self.assertTrue(store.retract("b"))
        self.assertFalse(store.retract("b"))
        self.assertEqual(store.total_weight(), 7)
        self.assertEqual(list(store.items()), [(1, Fraction(2)), (3, Fraction(5))])

    def test_retract_subject(self):
        store = WeightedStore(random.Random(0))
        store.upsert("a", 1, 1, subject="alice")
        store.upsert("b", 2, 1, subject="alice")
        store.upsert("c", 3, 1, subject="bob")
        self.assertEqual(store.retract_subject("alice"), 2)
        self.assertEqual(list(store.items()), [(3, Fraction(1))])


class SlidingWindowTests(unittest.TestCase):
    def test_expiry(self):
        store = WeightedStore(random.Random(0), window=10)
        store.upsert("old", 1, 1, timestamp=0)
        store.upsert("fresh", 2, 1, timestamp=8)
        expired = store.advance(11)  # cutoff = 1
        self.assertEqual(expired, ["old"])
        self.assertEqual(list(store.items()), [(2, Fraction(1))])

    def test_stale_heap_entries_ignored(self):
        store = WeightedStore(random.Random(0), window=10)
        store.upsert("a", 1, 1, timestamp=0)
        store.upsert("a", 1, 1, timestamp=50)  # re-timestamped
        self.assertEqual(store.advance(20), [])  # cutoff=10, old heap entry stale
        self.assertEqual(len(store), 1)

    def test_no_window_no_expiry(self):
        store = WeightedStore(random.Random(0))
        store.upsert("a", 1, 1, timestamp=0)
        self.assertEqual(store.advance(10**9), [])
        self.assertEqual(len(store), 1)


class ShardMergeTests(unittest.TestCase):
    def test_split_merge_roundtrip(self):
        rng = random.Random(7)
        store = WeightedStore(random.Random(1))
        for i in range(200):
            store.upsert(f"o{i}", rng.randint(0, 30),
                         Fraction(rng.randint(1, 9), rng.randint(1, 5)))
        shards = split_shards(store, 4, seed=3)
        self.assertEqual(sum(len(s) for s in shards), len(store))
        merged = merge_shards(shards)
        self.assertEqual(list(merged.items()), list(store.items()))
        for q in (Fraction(0), Fraction(1, 3), Fraction(1, 2), Fraction(1)):
            self.assertEqual(merged.quantile(q), store.quantile(q))

    def test_merge_idempotent_duplicates(self):
        a = make_store(x=(1, 1), y=(2, 2))
        b = make_store(y=(2, 2), z=(3, 3))  # y identical in both
        merged = a.merge(b)
        self.assertEqual(list(merged.items()),
                         [(1, Fraction(1)), (2, Fraction(2)), (3, Fraction(3))])

    def test_merge_conflicting_duplicates_raise(self):
        a = make_store(x=(1, 1))
        b = make_store(x=(1, 2))
        with self.assertRaises(ValueError):
            a.merge(b)


class DegeneracyTests(unittest.TestCase):
    def test_equal_weights_match_inverse_ecdf_definition(self):
        # With equal weights the quantile degenerates to the ordinary
        # "inverse empirical CDF" definition (Hyndman-Fan type 1), with
        # intervals at exact cumulative boundaries.
        rng = random.Random(42)
        for trial in range(30):
            n = rng.randint(1, 40)
            sample = [rng.randint(0, 50) for _ in range(n)]
            store = WeightedStore(random.Random(trial))
            for i, v in enumerate(sample):
                store.upsert(i, v, 1)
            ordered = sorted(sample)
            distinct = sorted(set(sample))
            cum_at = {}  # distinct value -> cumulative count (inclusive)
            run = 0
            for v in distinct:
                run += sample.count(v)
                cum_at[v] = run
            for num in range(0, n + 1):
                q = Fraction(num, n)
                r = store.quantile(q)
                if num == 0:
                    self.assertEqual(r.low, ordered[0])
                elif num == n:
                    self.assertEqual(r.low, ordered[-1])
                else:
                    import math
                    rank = math.ceil(q * n)  # type-1 order statistic index
                    point = ordered[rank - 1]
                    if cum_at[point] == q * n and point != distinct[-1]:
                        # exact boundary -> interval to the next distinct value
                        nxt = distinct[distinct.index(point) + 1]
                        self.assertEqual((r.low, r.high), (point, nxt))
                    else:
                        self.assertFalse(r.is_interval)
                        self.assertEqual(r.low, point)


class InstrumentationTests(unittest.TestCase):
    def test_node_visits_sublinear(self):
        rng = random.Random(11)
        store = WeightedStore(random.Random(2))
        for i in range(1000):
            store.upsert(i, rng.random(), 1)
        store.reset_stats()
        for num in range(1, 100):
            store.quantile(Fraction(num, 100))
        # 99 quantile queries over 1000 distinct values must cost far fewer
        # node visits than a full scan per query would.
        self.assertLess(store.node_visits(), 99 * 100)
        self.assertGreater(store.node_visits(), 0)


class FuzzTests(unittest.TestCase):
    def test_random_transactions_match_reference(self):
        for seed in (1, 2, 3, 12345):
            report = run_random_transactions(seed=seed, n_ops=1500)
            self.assertTrue(report["ok"], report["mismatches"][:3])
            self.assertEqual(report["seed"], seed)
            self.assertEqual(report["samples"], 1500)
            self.assertGreater(report["treap_node_visits"], 0)
            self.assertGreater(report["reference_full_sorts"], 0)

    def test_caller_injected_rng_reproducible(self):
        r1 = run_random_transactions(seed=0, n_ops=300, rng=random.Random(99))
        r2 = run_random_transactions(seed=0, n_ops=300, rng=random.Random(99))
        self.assertEqual(r1["final_total_weight"], r2["final_total_weight"])
        self.assertEqual(r1["treap_node_visits"], r2["treap_node_visits"])


class ReferenceSelfTests(unittest.TestCase):
    def test_reference_rejects_negative_and_drops_zero(self):
        ref = NaiveWeightedReference()
        with self.assertRaises(ValueError):
            ref.upsert("a", 1, -1)
        ref.upsert("b", 1, 0)
        self.assertEqual(ref.total_weight(), 0)


if __name__ == "__main__":
    unittest.main()
