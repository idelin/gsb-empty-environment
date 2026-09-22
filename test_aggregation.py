"""两阶段聚合的单元测试（标准库 unittest）。

覆盖要点：
- AVG 保留 (sum, count)，不能平均分片均值
- 空分片不贡献分母
- 全 NULL 的 SUM 返回 None（NULL）
- NULL 语义与空表一致
- 多重集合（重复行）语义
- COUNT DISTINCT 的额外状态跨分片合并
"""

from __future__ import annotations

import random
import unittest

from aggregation import (
    AVG,
    COUNT,
    COUNT_DISTINCT,
    COUNT_STAR,
    MAX,
    MIN,
    SUM,
    AggSpec,
    distributed_aggregate,
    finalize,
    init_partial,
    local_phase,
    merge,
)


def rows(values):
    return [{"v": v} for v in values]


class TestAvgDecomposition(unittest.TestCase):
    def test_avg_keeps_sum_and_count(self):
        partials = local_phase(rows([10, 20, None]), [AggSpec(AVG, "v")])
        self.assertEqual(partials[0].total, 30)
        self.assertEqual(partials[0].count, 2)

    def test_avg_is_not_average_of_shard_averages(self):
        # 分片 1 只有 1 行，分片 2 有 2 行；均值之均值 (10+30)/2=20 是错的。
        shards = [rows([10]), rows([20, 40])]
        (result,) = distributed_aggregate(shards, [AggSpec(AVG, "v")])
        self.assertAlmostEqual(result, 70 / 3)
        self.assertNotAlmostEqual(result, 20.0)

    def test_empty_shard_contributes_no_denominator(self):
        shards = [rows([10, 20]), [], rows([None])]  # 空分片 + 全 NULL 分片
        (result,) = distributed_aggregate(shards, [AggSpec(AVG, "v")])
        self.assertAlmostEqual(result, 15.0)

    def test_avg_all_null_is_none(self):
        (result,) = distributed_aggregate(
            [rows([None, None]), []], [AggSpec(AVG, "v")]
        )
        self.assertIsNone(result)


class TestSumNullSemantics(unittest.TestCase):
    def test_all_null_sum_returns_none(self):
        (result,) = distributed_aggregate(
            [rows([None]), rows([None, None])], [AggSpec(SUM, "v")]
        )
        self.assertIsNone(result)

    def test_empty_table_sum_returns_none(self):
        (result,) = distributed_aggregate([[], []], [AggSpec(SUM, "v")])
        self.assertIsNone(result)

    def test_sum_ignores_nulls(self):
        (result,) = distributed_aggregate(
            [rows([1, None]), rows([None, 2])], [AggSpec(SUM, "v")]
        )
        self.assertEqual(result, 3)

    def test_null_semantics_match_empty_table(self):
        # 全 NULL 列的每个聚合结果 == 空表的结果。
        specs = [
            AggSpec(COUNT, "v"),
            AggSpec(SUM, "v"),
            AggSpec(AVG, "v"),
            AggSpec(MIN, "v"),
            AggSpec(MAX, "v"),
            AggSpec(COUNT_DISTINCT, "v"),
        ]
        all_null = distributed_aggregate([rows([None, None])], specs)
        empty = distributed_aggregate([[]], specs)
        self.assertEqual(all_null, empty)
        self.assertEqual(empty, [0, None, None, None, None, 0])


class TestMultisetSemantics(unittest.TestCase):
    def test_duplicate_rows_counted(self):
        table = rows([5, 5, 5])
        specs = [AggSpec(COUNT_STAR), AggSpec(SUM, "v"), AggSpec(AVG, "v")]
        count, total, avg = distributed_aggregate([table], specs)
        self.assertEqual((count, total), (3, 15))
        self.assertAlmostEqual(avg, 5.0)

    def test_count_star_vs_count_column_keeps_null_rows(self):
        table = rows([1, None, None])
        count_star, count_col = distributed_aggregate(
            [table], [AggSpec(COUNT_STAR), AggSpec(COUNT, "v")]
        )
        self.assertEqual(count_star, 3)
        self.assertEqual(count_col, 1)


class TestDistinctState(unittest.TestCase):
    def test_distinct_merges_across_shards(self):
        shards = [rows([1, 2, 2]), rows([2, 3]), rows([])]
        (result,) = distributed_aggregate(shards, [AggSpec(COUNT_DISTINCT, "v")])
        self.assertEqual(result, 3)

    def test_distinct_ignores_nulls(self):
        (result,) = distributed_aggregate(
            [rows([None, 1, None])], [AggSpec(COUNT_DISTINCT, "v")]
        )
        self.assertEqual(result, 1)

    def test_distinct_state_is_a_set(self):
        (partial,) = local_phase(rows([1, 1, 2]), [AggSpec(COUNT_DISTINCT, "v")])
        self.assertEqual(partial.distinct, {1, 2})


class TestMinMax(unittest.TestCase):
    def test_min_max_ignore_nulls_and_merge(self):
        shards = [rows([None, 7]), rows([3, None]), rows([])]
        lo, hi = distributed_aggregate(
            shards, [AggSpec(MIN, "v"), AggSpec(MAX, "v")]
        )
        self.assertEqual((lo, hi), (3, 7))

    def test_min_max_all_null(self):
        lo, hi = distributed_aggregate(
            [rows([None])], [AggSpec(MIN, "v"), AggSpec(MAX, "v")]
        )
        self.assertIsNone(lo)
        self.assertIsNone(hi)


class TestDistributedEqualsSingleTable(unittest.TestCase):
    def test_random_sharding_matches_single_table(self):
        rng = random.Random(42)
        table = rows(
            [rng.choice([None] + list(range(20))) for _ in range(200)]
        )
        specs = [
            AggSpec(COUNT_STAR),
            AggSpec(COUNT, "v"),
            AggSpec(SUM, "v"),
            AggSpec(AVG, "v"),
            AggSpec(MIN, "v"),
            AggSpec(MAX, "v"),
            AggSpec(COUNT_DISTINCT, "v"),
        ]
        expected = distributed_aggregate([table], specs)
        for n_shards in (1, 2, 3, 7, 50):  # 50 > 行数时会产生空分片
            shards = [table[i::n_shards] for i in range(n_shards)]
            got = distributed_aggregate(shards, specs)
            for want, have in zip(expected, got):
                if isinstance(want, float):
                    self.assertAlmostEqual(want, have)
                else:
                    self.assertEqual(want, have)


class TestMergeContract(unittest.TestCase):
    def test_merge_requires_matching_specs(self):
        a = init_partial(AggSpec(SUM, "v"))
        b = init_partial(AggSpec(AVG, "v"))
        with self.assertRaises(ValueError):
            merge(a, b)

    def test_merge_is_associative_and_commutative(self):
        shards = [rows([1, None]), rows([2, 3]), rows([]), rows([None, 4])]
        spec = AggSpec(AVG, "v")
        partials = [local_phase(s, [spec])[0] for s in shards]
        left = init_partial(spec)
        for p in partials:
            merge(left, p)
        right = init_partial(spec)
        for p in reversed(partials):
            merge(right, p)
        self.assertAlmostEqual(finalize(left), finalize(right))
        self.assertAlmostEqual(finalize(left), 10 / 4)


if __name__ == "__main__":
    unittest.main()
