import json
import math
import random
import unittest

from group_quota_sampler import (
    Column,
    GroupQuotaSampler,
    TableBatch,
    row_key,
)
from group_quota_sampler.encoding import canonical_value

SEED = 20260925
COLS = [Column("id", "int"), Column("dept", "str"), Column("score", "float")]
GROUPS = [("eng",), ("ops",), ("sci",)]


def make_rows(per_group=10):
    rows = []
    rid = 0
    for (dept,) in GROUPS:
        for i in range(per_group):
            rows.append((rid, dept, float(i)))
            rid += 1
    return rows


def make_sampler(quotas, seed=SEED, default_quota=0):
    return GroupQuotaSampler(
        seed=seed,
        columns=COLS,
        id_column="id",
        group_columns=("dept",),
        quotas=quotas,
        default_quota=default_quota,
    )


def feed(sampler, rows, batch_size=7, version=1):
    for i in range(0, len(rows), batch_size):
        sampler.add_batch(TableBatch(COLS, rows[i:i + batch_size]), version=version)


def full_reference(rows, quotas, seed=SEED, default_quota=0):
    """Independent full-data reference: one sampler, one batch."""
    s = make_sampler(quotas, seed=seed, default_quota=default_quota)
    s.add_batch(TableBatch(COLS, rows), version=1)
    return s.sample()


class TestDeterminism(unittest.TestCase):
    def test_batch_split_does_not_change_sample(self):
        rows = make_rows()
        quotas = {g: 3 for g in GROUPS}
        ref = full_reference(rows, quotas)
        for batch_size in (1, 2, 5, 7, 1000):
            s = make_sampler(quotas)
            feed(s, rows, batch_size=batch_size)
            self.assertEqual(s.sample(), ref)

    def test_input_order_does_not_change_sample(self):
        rows = make_rows()
        quotas = {g: 4 for g in GROUPS}
        ref = full_reference(rows, quotas)
        rng = random.Random(7)
        for _ in range(5):
            shuffled = rows[:]
            rng.shuffle(shuffled)
            s = make_sampler(quotas)
            feed(s, shuffled, batch_size=4)
            self.assertEqual(s.sample(), ref)

    def test_explicit_seed_changes_sample_but_is_stable(self):
        rows = make_rows()
        quotas = {g: 3 for g in GROUPS}
        a1 = full_reference(rows, quotas, seed=1)
        a2 = full_reference(rows, quotas, seed=1)
        b = full_reference(rows, quotas, seed=2)
        self.assertEqual(a1, a2)
        self.assertNotEqual(a1, b)


class TestQuotas(unittest.TestCase):
    def test_deficit_reported_for_undersized_group(self):
        rows = [(1, "eng", 1.0), (2, "eng", 2.0), (3, "ops", 1.0)]
        quotas = {("eng",): 5, ("ops",): 1, ("sci",): 2}
        result = full_reference(rows, quotas)
        by = result.by_group()
        eng = by[canonical_value_group(("eng",))]
        self.assertEqual((eng.quota, eng.actual, eng.deficit), (5, 2, 3))
        ops = by[canonical_value_group(("ops",))]
        self.assertEqual((ops.quota, ops.actual, ops.deficit), (1, 1, 0))
        sci = by[canonical_value_group(("sci",))]
        self.assertEqual((sci.quota, sci.actual, sci.deficit), (2, 0, 2))

    def test_quota_zero_is_defined(self):
        rows = make_rows(per_group=3)
        result = full_reference(rows, {g: 0 for g in GROUPS})
        for gr in result.groups:
            self.assertEqual((gr.quota, gr.actual, gr.deficit), (0, 0, 0))
            self.assertEqual(gr.row_ids, ())

    def test_quota_larger_than_group_is_defined(self):
        rows = make_rows(per_group=4)
        result = full_reference(rows, {g: 100 for g in GROUPS})
        for gr in result.groups:
            self.assertEqual((gr.quota, gr.actual, gr.deficit), (100, 4, 96))

    def test_quota_change_matches_independent_reference(self):
        rows = make_rows(per_group=12)
        s = make_sampler({g: 2 for g in GROUPS})
        feed(s, rows, batch_size=5)
        first = s.sample()
        self.assertEqual(first, full_reference(rows, {g: 2 for g in GROUPS}))
        for new_q in (5, 0, 9, 1):
            for g in GROUPS:
                s.set_quota(g, new_q)
            self.assertEqual(s.sample(), full_reference(rows, {g: new_q for g in GROUPS}))


def canonical_value_group(group):
    return tuple(canonical_value(v) for v in group)


class TestTypedValues(unittest.TestCase):
    def test_null_and_nan_are_distinct_groups(self):
        cols = [Column("id", "int"), Column("g", "float")]
        rows = [(1, None), (2, float("nan")), (3, 1.5), (4, None)]
        s = GroupQuotaSampler(seed=SEED, columns=cols, id_column="id",
                              group_columns=("g",), default_quota=10)
        s.add_batch(TableBatch(cols, rows), version=1)
        result = s.sample()
        self.assertEqual(len(result.groups), 3)  # null, NaN, 1.5
        by = result.by_group()
        self.assertEqual(by[(canonical_value(None),)].row_ids, (1, 4))
        self.assertEqual(by[(canonical_value(float("nan")),)].row_ids, (2,))

    def test_integers_are_exact(self):
        big1, big2 = 2**63 + 1, 2**63 + 2  # indistinguishable as float64
        cols = [Column("id", "int"), Column("g", "int")]
        rows = [(big1, big1), (big2, big2)]
        s = GroupQuotaSampler(seed=SEED, columns=cols, id_column="id",
                              group_columns=("g",), default_quota=5)
        s.add_batch(TableBatch(cols, rows), version=1)
        result = s.sample()
        self.assertEqual(len(result.groups), 2)
        keys = {row_key(b"s", big1), row_key(b"s", big2)}
        self.assertEqual(len(keys), 2)

    def test_schema_mismatch_rejected(self):
        s = make_sampler({})
        with self.assertRaises(TypeError):
            TableBatch(COLS, [(1, "eng", "not-a-float")])  # str in a float column
        other = [Column("id", "int"), Column("dept", "str"), Column("score", "str")]
        with self.assertRaises(ValueError):
            s.add_batch(TableBatch(other, [(1, "eng", "x")]), version=1)
        with self.assertRaises(TypeError):
            TableBatch(COLS, [(True, "eng", 1.0)])  # bool is not an int


class TestShardMerge(unittest.TestCase):
    def test_merge_equals_full_reference_for_random_shardings(self):
        rows = make_rows(per_group=9)
        quotas = {GROUPS[0]: 3, GROUPS[1]: 0, GROUPS[2]: 50}
        ref = full_reference(rows, quotas)
        rng = random.Random(11)
        for trial in range(20):
            shuffled = rows[:]
            rng.shuffle(shuffled)
            n_shards = rng.randint(1, 6)
            shards = [[] for _ in range(n_shards)]
            for row in shuffled:
                shards[rng.randrange(n_shards)].append(row)
            exports = []
            for shard_rows in shards:
                s = make_sampler(quotas)
                s.add_batch(TableBatch(COLS, shard_rows), version=1)
                exports.append(s.export_shard())
            merged = GroupQuotaSampler.merge_shards(exports)
            self.assertEqual(merged, ref, f"trial {trial}")

    def test_merge_is_not_naive_concat_and_truncate(self):
        # Place the two globally-best rows of one group in different shards so
        # that "concat shard samples, take first k" provably diverges.
        quotas = {("eng",): 2}
        rows = [(i, "eng", 0.0) for i in range(8)]
        keys = sorted(range(8), key=lambda i: row_key(
            __import__("group_quota_sampler.encoding", fromlist=["normalize_seed"])
            .normalize_seed(SEED), i))
        # keys[0], keys[1] are the two smallest keys globally
        shard_a = [rows[keys[0]], rows[keys[4]], rows[keys[5]]]
        shard_b = [rows[keys[1]], rows[keys[2]], rows[keys[3]],
                   rows[keys[6]], rows[keys[7]]]
        exports = []
        for shard in (shard_a, shard_b):
            s = make_sampler(quotas)
            s.add_batch(TableBatch(COLS, shard), version=1)
            exports.append(s.export_shard())
        merged = GroupQuotaSampler.merge_shards(exports)
        ref = full_reference(rows, quotas)
        self.assertEqual(merged, ref)
        (eng,) = merged.groups
        self.assertEqual(set(eng.row_ids), {keys[0], keys[1]})
        # naive concat-truncate would keep keys[0] and keys[4] from shard A
        naive = (keys[0], keys[4])
        self.assertNotEqual(tuple(sorted(eng.row_ids)), tuple(sorted(naive)))


class TestRetraction(unittest.TestCase):
    def test_deleted_sampled_row_is_replenished(self):
        rows = make_rows(per_group=6)
        quotas = {g: 3 for g in GROUPS}
        s = make_sampler(quotas)
        feed(s, rows, batch_size=4)
        before = s.sample()
        victim = before.groups[0].row_ids[0]
        s.retract([victim], version=2)
        after = s.sample()
        self.assertNotIn(victim, after.groups[0].row_ids)
        self.assertEqual(after.groups[0].actual, 3)  # replenished
        # equals the independent reference over the remaining visible rows
        visible = [r for r in rows if r[0] != victim]
        self.assertEqual(after, full_reference(visible, quotas))

    def test_retracting_unsampled_row_keeps_sample(self):
        rows = make_rows(per_group=6)
        quotas = {g: 2 for g in GROUPS}
        s = make_sampler(quotas)
        feed(s, rows)
        before = s.sample()
        sampled = {rid for g in before.groups for rid in g.row_ids}
        outsider = next(r[0] for r in rows if r[0] not in sampled)
        s.retract([outsider], version=2)
        self.assertEqual(s.sample(), before)

    def test_versioned_retraction_semantics(self):
        rows = [(1, "eng", 1.0)]
        quotas = {("eng",): 1}
        s = make_sampler(quotas)
        s.add_batch(TableBatch(COLS, rows), version=5)
        s.retract([1], version=3)   # older than the insert: no effect
        self.assertEqual(s.sample().groups[0].actual, 1)
        s.retract([1], version=5)   # same version: retracted
        self.assertEqual(s.sample().groups[0].actual, 0)
        s.add_batch(TableBatch(COLS, rows), version=6)  # newer insert revives
        self.assertEqual(s.sample().groups[0].actual, 1)
        s.retract([1], version=9)
        self.assertEqual(s.sample().groups[0].actual, 0)

    def test_retraction_before_insert_arrives(self):
        s = make_sampler({("eng",): 2})
        s.retract([1], version=4)               # tombstone first
        s.add_batch(TableBatch(COLS, [(1, "eng", 1.0)]), version=2)  # stale
        self.assertEqual(s.sample().groups[0].actual, 0)
        s.add_batch(TableBatch(COLS, [(1, "eng", 1.0)]), version=7)  # newer
        self.assertEqual(s.sample().groups[0].actual, 1)


class TestCheckpoint(unittest.TestCase):
    def test_checkpoint_restore_equals_full_reference(self):
        rows = make_rows(per_group=10)
        quotas = {GROUPS[0]: 4, GROUPS[1]: 2, GROUPS[2]: 7}
        s = make_sampler(quotas)
        feed(s, rows[:15], batch_size=3)
        s.retract([rows[2][0], rows[9][0]], version=2)
        state = json.loads(json.dumps(s.checkpoint()))  # JSON round-trip
        restored = GroupQuotaSampler.restore(state)
        feed(restored, rows[15:], batch_size=4)
        restored.retract([rows[20][0]], version=3)
        visible = [r for i, r in enumerate(rows) if i not in (2, 9, 20)]
        self.assertEqual(restored.sample(), full_reference(visible, quotas))

    def test_restore_then_quota_change_equals_reference(self):
        rows = make_rows(per_group=8)
        s = make_sampler({g: 1 for g in GROUPS})
        feed(s, rows)
        restored = GroupQuotaSampler.restore(json.loads(json.dumps(s.checkpoint())))
        for g in GROUPS:
            restored.set_quota(g, 5)
        self.assertEqual(restored.sample(), full_reference(rows, {g: 5 for g in GROUPS}))


class TestInclusionProbability(unittest.TestCase):
    def test_small_domain_exhaustive_key_rank(self):
        # Exhaustively verify, for every id in a small domain and many seeds,
        # that inclusion <=> the id's key is among the k smallest.
        ids = list(range(12))
        k = 4
        for seed in range(50):
            s = GroupQuotaSampler(seed=seed, columns=COLS, id_column="id",
                                  group_columns=("dept",), quotas={("eng",): k})
            s.add_batch(TableBatch(COLS, [(i, "eng", 0.0) for i in ids]), version=1)
            (gr,) = s.sample().groups
            from group_quota_sampler.encoding import normalize_seed
            sb = normalize_seed(seed)
            expected = sorted(ids, key=lambda i: row_key(sb, i))[:k]
            self.assertEqual(sorted(gr.row_ids), sorted(expected))
            # every id in the domain: included iff its key ranks in the top k
            for i in ids:
                rank = sorted(ids, key=lambda j: row_key(sb, j)).index(i)
                self.assertEqual(i in gr.row_ids, rank < k)

    def test_inclusion_frequency_matches_quota_over_size(self):
        n, k, seeds = 10, 3, 4000
        counts = [0] * n
        for seed in range(seeds):
            s = GroupQuotaSampler(seed=seed, columns=COLS, id_column="id",
                                  group_columns=("dept",), quotas={("eng",): k})
            s.add_batch(TableBatch(COLS, [(i, "eng", 0.0) for i in range(n)]), version=1)
            (gr,) = s.sample().groups
            self.assertEqual(gr.actual, k)
            for rid in gr.row_ids:
                counts[rid] += 1
        mean = seeds * k / n
        sigma = math.sqrt(seeds * (k / n) * (1 - k / n))
        for c in counts:
            self.assertLess(abs(c - mean), 5 * sigma)


if __name__ == "__main__":
    unittest.main()
