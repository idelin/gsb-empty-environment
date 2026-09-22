"""Unit tests for distagg: decomposition, NULL and multiset semantics."""

import unittest

from distagg import (
    Aggregate,
    Avg,
    Count,
    CountStar,
    Max,
    Min,
    Sum,
    Table,
    direct_aggregate,
    distributed_aggregate,
    local_aggregate,
    merge_states,
)


def employees():
    """Synthesized table with NULLs and duplicate rows (multiset)."""
    return Table(
        ("id", "dept", "salary", "bonus"),
        [
            (1, "eng", 100, 10),
            (2, "eng", 200, None),
            (3, "ops", None, 30),
            (4, "ops", 200, 30),
            (5, None, 300, None),
            (6, "eng", 200, None),   # duplicate salary across rows
            (6, "eng", 200, None),   # fully duplicated row: counts twice
        ],
    )


class NullSemanticsTest(unittest.TestCase):
    def test_all_null_sum_returns_null(self):
        t = Table(("x",), [(None,), (None,), (None,)])
        self.assertIsNone(direct_aggregate(t, [Sum("x")])[0])

    def test_all_null_sum_returns_null_distributed(self):
        t = Table(("x",), [(None,), (None,)])
        self.assertIsNone(distributed_aggregate(t.shard(2), [Sum("x")])[0])

    def test_sum_ignores_nulls(self):
        t = Table(("x",), [(1,), (None,), (2,)])
        self.assertEqual(direct_aggregate(t, [Sum("x")])[0], 3)

    def test_count_star_counts_rows_with_nulls(self):
        t = Table(("x",), [(1,), (None,), (None,)])
        self.assertEqual(direct_aggregate(t, [CountStar()])[0], 3)

    def test_count_column_skips_nulls(self):
        t = Table(("x",), [(1,), (None,), (None,)])
        self.assertEqual(direct_aggregate(t, [Count("x")])[0], 1)

    def test_avg_ignores_nulls(self):
        t = Table(("x",), [(10,), (None,), (20,)])
        self.assertEqual(direct_aggregate(t, [Avg("x")])[0], 15)

    def test_min_max_ignore_nulls(self):
        t = Table(("x",), [(None,), (5,), (None,), (2,)])
        self.assertEqual(direct_aggregate(t, [Min("x"), Max("x")]), [2, 5])

    def test_min_max_all_null_return_null(self):
        t = Table(("x",), [(None,), (None,)])
        self.assertEqual(direct_aggregate(t, [Min("x"), Max("x")]), [None, None])


class EmptyTableTest(unittest.TestCase):
    def setUp(self):
        self.empty = Table(("x", "y"), [])

    def test_count_star_on_empty_table_is_zero(self):
        self.assertEqual(direct_aggregate(self.empty, [CountStar()])[0], 0)

    def test_count_on_empty_table_is_zero(self):
        self.assertEqual(direct_aggregate(self.empty, [Count("x")])[0], 0)

    def test_sum_avg_min_max_on_empty_table_are_null(self):
        aggs = [Sum("x"), Avg("x"), Min("x"), Max("x")]
        self.assertEqual(direct_aggregate(self.empty, aggs), [None] * 4)

    def test_count_distinct_on_empty_table_is_zero(self):
        self.assertEqual(
            direct_aggregate(self.empty, [Count("x", distinct=True)])[0], 0
        )

    def test_all_null_column_behaves_like_empty_for_sum(self):
        # NULL semantics consistent with empty-table semantics
        all_null = Table(("x",), [(None,), (None,)])
        self.assertEqual(
            direct_aggregate(all_null, [Sum("x"), Avg("x"), Count("x")]),
            direct_aggregate(self.empty, [Sum("x"), Avg("x"), Count("x")]),
        )


class DecompositionTest(unittest.TestCase):
    def test_avg_is_not_mean_of_shard_means(self):
        # shard 1: one big value; shard 2: many small values
        t = Table(("x",), [(100,)] + [(1,)] * 9)
        shards = t.shard(3)  # unequal shard sizes: 4, 3, 3
        shard_means = [
            direct_aggregate(s, [Avg("x")])[0] for s in shards if len(s) > 0
        ]
        naive = sum(shard_means) / len(shard_means)
        correct = distributed_aggregate(shards, [Avg("x")])[0]
        self.assertNotAlmostEqual(naive, correct)
        self.assertAlmostEqual(correct, 109 / 10)

    def test_avg_state_carries_sum_and_count(self):
        t = Table(("x",), [(10,), (20,), (None,)])
        (state,) = local_aggregate(t, [Avg("x")])
        self.assertEqual(state, (30, 2))

    def test_empty_shard_does_not_contribute_to_denominator(self):
        full = Table(("x",), [(10,), (20,)])
        empty = Table(("x",), [])
        result = distributed_aggregate([full, empty], [Avg("x")])[0]
        self.assertEqual(result, 15)  # 30/2, not 30/3 or similar

    def test_all_empty_shards_avg_is_null(self):
        empty = Table(("x",), [])
        self.assertIsNone(distributed_aggregate([empty, empty], [Avg("x")])[0])

    def test_distributed_matches_direct_for_all_aggregates(self):
        t = employees()
        aggs = [
            CountStar(),
            Count("bonus"),
            Sum("salary"),
            Avg("salary"),
            Min("salary"),
            Max("salary"),
            Count("dept", distinct=True),
            Sum("bonus", distinct=True),
            Avg("salary", distinct=True),
        ]
        for n_shards in (1, 2, 3, 5, 10):  # 10 shards -> some empty
            with self.subTest(n_shards=n_shards):
                self.assertEqual(
                    distributed_aggregate(t.shard(n_shards), aggs),
                    direct_aggregate(t, aggs),
                )

    def test_merge_is_associative_and_commutative(self):
        t = employees()
        aggs = [Sum("salary"), Avg("salary"), Count("dept", distinct=True)]
        shards = t.shard(4)
        states = [local_aggregate(s, aggs) for s in shards]
        forward = merge_states(states, aggs)
        reverse = merge_states(reversed(states), aggs)
        regrouped = merge_states(
            [merge_states(states[:2], aggs), merge_states(states[2:], aggs)],
            aggs,
        )
        self.assertEqual(forward, reverse)
        self.assertEqual(forward, regrouped)


class MultisetAndDistinctTest(unittest.TestCase):
    def test_duplicate_rows_counted_per_occurrence(self):
        t = Table(("x",), [(5,), (5,), (5,)])
        self.assertEqual(
            direct_aggregate(t, [CountStar(), Sum("x"), Count("x")]),
            [3, 15, 3],
        )

    def test_count_distinct_dedups_within_and_across_shards(self):
        t = Table(("x",), [(1,), (2,), (2,), (1,), (None,), (3,)])
        shards = t.shard(3)  # duplicates land in different shards
        self.assertEqual(
            distributed_aggregate(shards, [Count("x", distinct=True)])[0], 3
        )

    def test_sum_distinct(self):
        t = Table(("x",), [(10,), (10,), (20,), (None,)])
        self.assertEqual(
            distributed_aggregate(t.shard(2), [Sum("x", distinct=True)])[0], 30
        )

    def test_avg_distinct(self):
        t = Table(("x",), [(10,), (10,), (40,), (None,)])
        self.assertEqual(
            distributed_aggregate(t.shard(2), [Avg("x", distinct=True)])[0], 25
        )

    def test_distinct_state_is_a_set_of_values(self):
        t = Table(("x",), [(1,), (2,), (2,), (None,)])
        (state,) = local_aggregate(t, [Count("x", distinct=True)])
        self.assertEqual(state, frozenset({1, 2}))

    def test_distinct_ignores_nulls(self):
        t = Table(("x",), [(None,), (None,), (7,)])
        self.assertEqual(
            direct_aggregate(t, [Count("x", distinct=True)])[0], 1
        )


class TableTest(unittest.TestCase):
    def test_row_width_validated(self):
        with self.assertRaises(ValueError):
            Table(("a", "b"), [(1,)])

    def test_duplicate_columns_rejected(self):
        with self.assertRaises(ValueError):
            Table(("a", "a"), [(1, 2)])

    def test_shard_preserves_multiset(self):
        t = employees()
        shards = t.shard(3)
        self.assertEqual(sum(len(s) for s in shards), len(t))
        self.assertEqual(sorted(r for s in shards for r in s.rows), sorted(t.rows))

    def test_more_shards_than_rows_yields_empty_shards(self):
        t = Table(("x",), [(1,), (2,)])
        shards = t.shard(4)
        self.assertEqual(len(shards), 4)
        self.assertEqual(sum(1 for s in shards if len(s) == 0), 2)


if __name__ == "__main__":
    unittest.main()
