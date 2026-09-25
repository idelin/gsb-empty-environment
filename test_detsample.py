"""unittest suite for detsample.  Run: python3.11 -m unittest -v"""

import math
import unittest

from detsample import (
    Batch,
    Column,
    Sampler,
    encode_value,
    is_nan,
    values_equal,
)
from detsample.sampler import default_key_fn

SEED = 20260925


def encode_results(results):
    """Canonical, hashable view of sample() output (NaN-safe)."""
    return tuple(
        (
            encode_value(r.group),
            r.quota,
            tuple(encode_value(i) for i in r.row_ids),
            tuple(tuple(encode_value(v) for v in row) for row in r.rows),
            r.actual,
            r.shortfall,
        )
        for r in results
    )


def result_by_group(results, group):
    gkey = encode_value(group)
    for r in results:
        if encode_value(r.group) == gkey:
            return r
    raise KeyError(group)


def make_rows(n, group="g", start=0):
    return [(start + i, group, float(start + i) / 10.0) for i in range(n)]


COLS = [Column("id", "int"), Column("grp", "str"), Column("score", "float")]


def feed(sampler, rows, version=0):
    sampler.add_batch(Batch(COLS, rows), id_col="id", group_col="grp")


class TestDeterminism(unittest.TestCase):
    def test_batch_split_invariance(self):
        rows = make_rows(60, "a") + make_rows(40, "b", start=60)
        one = Sampler(SEED, {"a": 5, "b": 5})
        feed(one, rows)
        many = Sampler(SEED, {"a": 5, "b": 5})
        for i in range(0, len(rows), 7):  # arbitrary split
            feed(many, rows[i:i + 7])
        self.assertEqual(encode_results(one.sample()), encode_results(many.sample()))

    def test_input_order_invariance(self):
        rows = make_rows(50, "a") + make_rows(50, "b", start=50)
        ref = Sampler(SEED, {"a": 8, "b": 8})
        feed(ref, rows)
        shuffled = rows[::3] + rows[1::3] + rows[2::3]  # deterministic reorder
        other = Sampler(SEED, {"a": 8, "b": 8})
        for i in range(0, len(shuffled), 9):
            feed(other, shuffled[i:i + 9])
        self.assertEqual(encode_results(ref.sample()), encode_results(other.sample()))

    def test_key_depends_only_on_seed_and_row_id(self):
        self.assertEqual(default_key_fn(1, "x"), default_key_fn(1, "x"))
        self.assertNotEqual(default_key_fn(1, "x"), default_key_fn(2, "x"))
        self.assertNotEqual(default_key_fn(1, "x"), default_key_fn(1, "y"))


class TestQuotas(unittest.TestCase):
    def test_shortfall_reported(self):
        s = Sampler(SEED, {"small": 10, "big": 3})
        feed(s, make_rows(4, "small") + make_rows(30, "big"))
        res = s.sample()
        small = result_by_group(res, "small")
        big = result_by_group(res, "big")
        self.assertEqual((small.actual, small.shortfall), (4, 6))
        self.assertEqual((big.actual, big.shortfall), (3, 0))

    def test_quota_zero(self):
        s = Sampler(SEED, {"g": 0})
        feed(s, make_rows(10))
        (r,) = s.sample()
        self.assertEqual((r.actual, r.shortfall, r.rows), (0, 0, ()))

    def test_quota_larger_than_group(self):
        s = Sampler(SEED, {"g": 100})
        feed(s, make_rows(6))
        (r,) = s.sample()
        self.assertEqual((r.actual, r.shortfall), (6, 94))
        self.assertEqual(len(r.rows), 6)

    def test_group_without_quota_defaults_to_zero(self):
        s = Sampler(SEED, {})
        feed(s, make_rows(5))
        (r,) = s.sample()
        self.assertEqual((r.quota, r.actual, r.shortfall), (0, 0, 0))

    def test_quota_change_matches_fresh_reference(self):
        rows = make_rows(30)
        s = Sampler(SEED, {"g": 2})
        feed(s, rows)
        self.assertEqual(result_by_group(s.sample(), "g").actual, 2)
        s.set_quota("g", 7)
        ref = Sampler(SEED, {"g": 7})
        feed(ref, rows)
        self.assertEqual(encode_results(s.sample()), encode_results(ref.sample()))
        s.set_quota("g", 0)
        self.assertEqual(result_by_group(s.sample(), "g").actual, 0)

    def test_invalid_quota_rejected(self):
        s = Sampler(SEED)
        for bad in (-1, 1.5, True, "3"):
            with self.assertRaises(ValueError):
                s.set_quota("g", bad)


class TestShardMerge(unittest.TestCase):
    def _sharded(self, rows, quotas, n_shards=3, seed=SEED):
        shards = []
        for i in range(n_shards):
            part = Sampler(seed, quotas)
            feed(part, rows[i::n_shards])
            shards.append(part.export_shard())
        return Sampler.from_shards(seed, quotas, shards)

    def test_merge_equals_full_reference(self):
        rows = make_rows(40, "a") + make_rows(25, "b", start=40)
        quotas = {"a": 6, "b": 4}
        ref = Sampler(SEED, quotas)
        feed(ref, rows)
        merged = self._sharded(rows, quotas)
        self.assertEqual(encode_results(ref.sample()), encode_results(merged.sample()))

    def test_merge_is_not_concat_then_truncate(self):
        # Naive (forbidden) approach: concatenate per-shard samples, take the
        # first `quota`.  Correct merge: k smallest keys of the union.
        rows = make_rows(12)
        quotas = {"g": 3}
        found_counterexample = False
        for seed in range(50):
            shard_samples = []
            for i in range(2):
                part = Sampler(seed, quotas)
                feed(part, rows[i::2])
                shard_samples.append(result_by_group(part.sample(), "g").row_ids)
            naive = (shard_samples[0] + shard_samples[1])[:3]
            merged = Sampler.from_shards(
                seed, quotas,
                [Sampler(seed, quotas) for _ in []] or
                [self._shard_export(seed, quotas, rows[i::2]) for i in range(2)],
            )
            correct = result_by_group(merged.sample(), "g").row_ids
            ref = Sampler(seed, quotas)
            feed(ref, rows)
            self.assertEqual(correct, result_by_group(ref.sample(), "g").row_ids)
            if tuple(sorted(naive)) != tuple(sorted(correct)) or naive != correct:
                found_counterexample = True
        self.assertTrue(found_counterexample,
                        "concat+truncate should diverge from correct merge")

    @staticmethod
    def _shard_export(seed, quotas, rows):
        part = Sampler(seed, quotas)
        feed(part, rows)
        return part.export_shard()

    def test_merge_seed_mismatch_rejected(self):
        a = Sampler(1, {"g": 1})
        feed(a, make_rows(3))
        with self.assertRaises(ValueError):
            Sampler.from_shards(2, {"g": 1}, [a.export_shard()])

    def test_merge_with_retraction_equals_full_reference(self):
        rows = make_rows(30)
        quotas = {"g": 5}
        shards = []
        for i in range(3):
            part = Sampler(SEED, quotas)
            feed(part, rows[i::3])
            if i == 0:
                part.retract(rows[0][0], 0)  # retract a row living in shard 0
            if i == 1:
                part.retract(rows[1][0], 0)  # tombstone for a row in shard 1
            shards.append(part.export_shard())
        merged = Sampler.from_shards(SEED, quotas, shards)
        ref = Sampler(SEED, quotas)
        feed(ref, rows)
        ref.retract(rows[0][0], 0)
        ref.retract(rows[1][0], 0)
        self.assertEqual(encode_results(ref.sample()), encode_results(merged.sample()))


class TestRetraction(unittest.TestCase):
    def test_retracted_sampled_row_is_replenished(self):
        rows = make_rows(10)
        quotas = {"g": 3}
        s = Sampler(SEED, quotas)
        feed(s, rows)
        first = result_by_group(s.sample(), "g")
        victim = first.row_ids[0]
        s.retract(victim, 0)
        second = result_by_group(s.sample(), "g")
        self.assertEqual(second.actual, 3)  # replenished from candidates
        self.assertNotIn(victim, second.row_ids)
        ref = Sampler(SEED, quotas)
        feed(ref, [r for r in rows if r[0] != victim])
        self.assertEqual(encode_results([second]),
                         encode_results([result_by_group(ref.sample(), "g")]))

    def test_versioned_retraction(self):
        quotas = {"g": 1}
        s = Sampler(SEED, quotas)
        s.add_row(1, "g", version=1)
        s.retract(1, 1)
        self.assertEqual(result_by_group(s.sample(), "g").actual, 0)
        s.add_row(1, "g", version=2)      # newer version visible again
        self.assertEqual(result_by_group(s.sample(), "g").actual, 1)
        s.retract(1, 1)                    # stale tombstone: no effect
        self.assertEqual(result_by_group(s.sample(), "g").actual, 1)
        s.retract(1, 2)                    # current tombstone hides v2
        self.assertEqual(result_by_group(s.sample(), "g").actual, 0)

    def test_tombstone_before_row_arrival(self):
        s = Sampler(SEED, {"g": 5})
        s.retract(7, 3)                    # retraction arrives first
        s.add_row(7, "g", version=2)       # v2 <= 3 -> hidden
        self.assertEqual(result_by_group(s.sample(), "g").actual, 0)
        s.add_row(7, "g", version=4)       # v4 > 3 -> visible
        self.assertEqual(result_by_group(s.sample(), "g").actual, 1)

    def test_newer_version_wins_across_shards(self):
        quotas = {"g": 1}
        a = Sampler(SEED, quotas)
        a.add_row(1, "g", values=("old",), version=1)
        b = Sampler(SEED, quotas)
        b.add_row(1, "g", values=("new",), version=2)
        merged = Sampler.from_shards(SEED, quotas,
                                     [a.export_shard(), b.export_shard()])
        (r,) = merged.sample()
        self.assertEqual(r.rows, (("new",),))


class TestCheckpoint(unittest.TestCase):
    def test_checkpoint_restore_equals_full_reference(self):
        rows = make_rows(25, "a") + make_rows(25, "b", start=25)
        quotas = {"a": 4, "b": 4}
        live = Sampler(SEED, quotas)
        feed(live, rows[:20])
        live.retract(rows[3][0], 0)
        data = live.checkpoint()

        restored = Sampler.restore(data)
        feed(restored, rows[20:])
        restored.retract(rows[40][0], 0)

        ref = Sampler(SEED, quotas)
        feed(ref, rows)
        ref.retract(rows[3][0], 0)
        ref.retract(rows[40][0], 0)
        self.assertEqual(encode_results(ref.sample()),
                         encode_results(restored.sample()))

    def test_restore_then_continue_ingest(self):
        quotas = {"g": 3}
        s = Sampler(SEED, quotas)
        feed(s, make_rows(5))
        restored = Sampler.restore(s.checkpoint())
        feed(restored, make_rows(5, start=5))
        ref = Sampler(SEED, quotas)
        feed(ref, make_rows(10))
        self.assertEqual(encode_results(ref.sample()),
                         encode_results(restored.sample()))


class TestTypedValues(unittest.TestCase):
    def test_null_and_nan_are_distinct(self):
        self.assertTrue(values_equal(None, None))
        self.assertTrue(values_equal(float("nan"), float("nan")))
        self.assertFalse(values_equal(None, float("nan")))
        self.assertNotEqual(encode_value(None), encode_value(float("nan")))

    def test_null_and_nan_groups_are_distinct(self):
        cols = [Column("id", "int"), Column("grp", "any")]
        s = Sampler(SEED, {None: 1, float("nan"): 1})
        s.add_batch(Batch(cols, [(1, None), (2, None),
                                 (3, float("nan")), (4, float("nan"))]),
                    id_col="id", group_col="grp")
        res = s.sample()
        self.assertEqual(len(res), 2)
        self.assertEqual(result_by_group(res, None).actual, 1)
        self.assertEqual(result_by_group(res, float("nan")).actual, 1)

    def test_nan_row_id_distinct_from_null_row_id(self):
        s = Sampler(SEED, {"g": 5})
        s.add_row(None, "g")
        s.add_row(float("nan"), "g")
        (r,) = s.sample()
        self.assertEqual(r.actual, 2)

    def test_integer_exactness(self):
        big_a, big_b = 2**53, 2**53 + 1  # indistinguishable as float64
        self.assertNotEqual(default_key_fn(SEED, big_a), default_key_fn(SEED, big_b))
        s = Sampler(SEED, {"g": 5})
        s.add_row(big_a, "g")
        s.add_row(big_b, "g")
        s.add_row(2**70 + 12345, "g")
        (r,) = s.sample()
        self.assertEqual(r.actual, 3)
        self.assertNotEqual(encode_value(2), encode_value(2.0))  # int != float
        self.assertNotEqual(encode_value(True), encode_value(1))  # bool != int

    def test_batch_type_validation(self):
        with self.assertRaises(TypeError):
            Batch([Column("i", "int")], [(1.5,)])
        with self.assertRaises(TypeError):
            Batch([Column("i", "int")], [(True,)])
        with self.assertRaises(TypeError):
            Batch([Column("f", "float")], [(1,)])  # int not accepted as float
        with self.assertRaises(ValueError):
            Batch([Column("i", "int")], [(1, 2)])
        # null and NaN both valid in a float column
        b = Batch([Column("f", "float")], [(None,), (float("nan"),)])
        self.assertIsNone(b.rows[0][0])
        self.assertTrue(is_nan(b.rows[1][0]))


class TestInclusionProbability(unittest.TestCase):
    def test_exhaustive_small_domain(self):
        # Exhaustive random-key check on a small domain.  The key space is
        # the full cycle of rotations: key = (row_id + seed) mod n, so over
        # ALL n seeds every row occupies every rank exactly once.  Each row
        # must therefore be sampled exactly `quota` times out of n seeds,
        # i.e. inclusion probability == quota / n exactly (no statistics).
        n = 6

        def rotation_key_fn(seed, row_id):
            return (row_id + seed) % n

        for quota in (0, 1, 2, 3, 5, 6, 7):  # incl. quota 0 and quota > n
            counts = {r: 0 for r in range(n)}
            for seed in range(n):
                s = Sampler(seed, {"g": quota}, key_fn=rotation_key_fn)
                for r in range(n):
                    s.add_row(r, "g")
                (res,) = s.sample()
                self.assertEqual(res.actual, min(quota, n))
                self.assertEqual(res.shortfall, max(0, quota - n))
                for rid in res.row_ids:
                    counts[rid] += 1
            self.assertEqual(counts, {r: min(quota, n) for r in range(n)})

    def test_sample_is_k_smallest_keys(self):
        s = Sampler(SEED, {"g": 4})
        for r in range(20):
            s.add_row(r, "g")
        (res,) = s.sample()
        expected = tuple(sorted(range(20),
                                key=lambda r: (default_key_fn(SEED, r),
                                               encode_value(r)))[:4])
        self.assertEqual(res.row_ids, expected)

    def test_statistical_inclusion_frequency(self):
        n, quota, trials = 10, 3, 2000
        counts = [0] * n
        for seed in range(trials):
            s = Sampler(seed, {"g": quota})
            for r in range(n):
                s.add_row(r, "g")
            (res,) = s.sample()
            for rid in res.row_ids:
                counts[rid] += 1
        p = quota / n
        for c in counts:
            self.assertAlmostEqual(c / trials, p, delta=0.06)


if __name__ == "__main__":
    unittest.main()
