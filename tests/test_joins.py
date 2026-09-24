import random
import unittest

from ival import (
    RecordBatch, Schema, Table, Interval, IntervalIndex,
    interval_interval_join, point_interval_join,
    sweep_interval_join, sweep_point_join,
    table_from_rows, values,
)



def iv_table(rows, types=None):
    """rows: list of (id, lo, hi)；默认 int64。"""
    types = types or [("id", "int64"), ("lo", "int64"), ("hi", "int64")]
    data = [{"id": i, "lo": lo, "hi": hi} for i, lo, hi in rows]
    return table_from_rows(types, data)


def pt_table(points, point_type=("p", "int64")):
    return table_from_rows([("id", "int64"), point_type],
                           [{"id": i, "p": p} for i, p in enumerate(points)])


def pairs(result):
    return [(m.left_id, m.right_id, m.overlap_len) for m in result.matches]


class SpecExamplesTest(unittest.TestCase):
    """题目明确给出的三条语义。"""

    def test_point_5_boundary(self):
        pts = pt_table([5])
        ivs = iv_table([(100, 0, 5), (101, 5, 8)])
        res = point_interval_join(pts, "id", "p", ivs, "id", "lo", "hi")
        self.assertEqual(pairs(res), [(0, 101, 0)])

    def test_overlap_length_two(self):
        left = iv_table([(1, 0, 7)])
        right = iv_table([(2, 3, 5)])
        res = interval_interval_join(left, "id", "lo", "hi",
                                     right, "id", "lo", "hi")
        self.assertEqual(pairs(res), [(1, 2, 2)])
        self.assertIsInstance(res.matches[0].overlap_len, int)

    def test_zero_length_interval_never_matches(self):
        left = iv_table([(1, 3, 3), (2, 5, 5)])
        right = iv_table([(9, 0, 100)])
        res = interval_interval_join(left, "id", "lo", "hi",
                                     right, "id", "lo", "hi")
        self.assertEqual(res.matches, [])
        self.assertEqual(res.left_stats.empty_interval_rows, 2)
        self.assertEqual(res.left_stats.used, 0)

    def test_all_algorithms_agree_on_spec(self):
        pts = pt_table([0, 3, 5, 7, 8])
        ivs = iv_table([(100, 0, 5), (101, 5, 8)])
        r1 = pairs(point_interval_join(pts, "id", "p", ivs, "id", "lo", "hi"))
        r2 = pairs(sweep_point_join(pts, "id", "p", ivs, "id", "lo", "hi"))
        self.assertEqual(r1, r2)

        left = iv_table([(1, 0, 7), (3, 7, 9)])
        right = iv_table([(2, 3, 5), (4, 5, 8), (5, 0, 9)])
        a = pairs(interval_interval_join(left, "id", "lo", "hi",
                                         right, "id", "lo", "hi"))
        b = pairs(sweep_interval_join(left, "id", "lo", "hi",
                                      right, "id", "lo", "hi"))
        self.assertEqual(a, b)


class BoundaryNestingTest(unittest.TestCase):
    def test_adjacent_half_open_not_overlap(self):
        left = iv_table([(1, 0, 5)])
        right = iv_table([(2, 5, 8)])
        res = interval_interval_join(left, "id", "lo", "hi",
                                     right, "id", "lo", "hi")
        self.assertEqual(res.matches, [])

    def test_duplicate_endpoints_and_ids(self):
        # 相同区间、重复 id 都应各自产出匹配
        pts = pt_table([2, 2])
        ivs = iv_table([(7, 0, 5), (7, 0, 5)])
        res = point_interval_join(pts, "id", "p", ivs, "id", "lo", "hi")
        self.assertEqual(sorted(pairs(res)),
                         sorted([(0, 7, 0), (0, 7, 0),
                                 (1, 7, 0), (1, 7, 0)]))

    def test_nesting(self):
        left = iv_table([(1, 0, 10)])
        right = iv_table([(2, 1, 2), (3, 2, 4), (4, 9, 10), (5, 10, 20)])
        res = interval_interval_join(left, "id", "lo", "hi",
                                     right, "id", "lo", "hi")
        self.assertEqual(pairs(res),
                         [(1, 2, 1), (1, 3, 2), (1, 4, 1)])

    def test_negative_and_identical_intervals(self):
        left = iv_table([(1, -5, 5)])
        right = iv_table([(2, -5, 5), (3, -6, -5)])
        res = interval_interval_join(left, "id", "lo", "hi",
                                     right, "id", "lo", "hi")
        self.assertEqual(pairs(res), [(1, 2, 10)])


class MissingValuesTest(unittest.TestCase):
    def test_null_distinct_from_nan(self):
        self.assertFalse(values.values_equal(None, None))
        self.assertFalse(values.values_equal(None, float("nan")))
        self.assertFalse(values.values_equal(float("nan"), float("nan")))
        self.assertTrue(values.is_null(None))
        self.assertFalse(values.is_null(float("nan")))
        self.assertTrue(values.is_nan(float("nan")))

    def test_null_and_nan_endpoints_skipped_separately(self):
        schema = Schema.from_pairs([("id", "int64"),
                                    ("lo", "float64"), ("hi", "float64")])
        nan = float("nan")
        batch = RecordBatch(schema, {
            "id": [1, 2, 3, 4],
            "lo": [0.0, None, 1.0, nan],
            "hi": [10.0, 5.0, nan, 9.0],
        })
        t = Table(schema, [batch])
        ref = iv_table([(9, 0, 10)])
        # float ref 表
        ref = table_from_rows([("id", "int64"), ("lo", "float64"),
                               ("hi", "float64")],
                              [{"id": 9, "lo": 0.0, "hi": 10.0}])
        res = interval_interval_join(t, "id", "lo", "hi",
                                     ref, "id", "lo", "hi")
        self.assertEqual(res.left_stats.used, 1)
        self.assertEqual(res.left_stats.null_endpoint_rows, 1)
        self.assertEqual(res.left_stats.nan_endpoint_rows, 2)
        self.assertEqual(pairs(res), [(1, 9, 10.0)])

    def test_int_column_rejects_float_and_bool(self):
        good = table_from_rows([("x", "int64")], [{"x": 1}])
        self.assertEqual(good.value("x", 0), 1)
        schema = Schema.from_pairs([("x", "int64")])
        with self.assertRaises(TypeError):
            RecordBatch(schema, {"x": [True]})
        with self.assertRaises(TypeError):
            RecordBatch(schema, {"x": [1.0]})

    def test_float_column_rejects_int(self):
        schema = Schema.from_pairs([("x", "float64")])
        with self.assertRaises(TypeError):
            RecordBatch(schema, {"x": [1]})

    def test_integer_exactness_large_values(self):
        big = 10**20
        left = iv_table([(1, big, big + 3)])
        right = iv_table([(2, big + 1, big + 10)])
        res = interval_interval_join(left, "id", "lo", "hi",
                                     right, "id", "lo", "hi")
        self.assertEqual(pairs(res), [(1, 2, 2)])
        self.assertIsInstance(res.matches[0].overlap_len, int)

    def test_cross_domain_rejected(self):
        left = iv_table([(1, 0, 5)])
        right = table_from_rows([("id", "int64"), ("lo", "float64"),
                                 ("hi", "float64")],
                                [{"id": 2, "lo": 2.0, "hi": 9.0}])
        with self.assertRaises(TypeError):
            interval_interval_join(left, "id", "lo", "hi",
                                   right, "id", "lo", "hi")


class BatchesTest(unittest.TestCase):
    def test_multi_batch_global_rows_and_stats(self):
        schema = Schema.from_pairs([("id", "int64"),
                                    ("lo", "int64"), ("hi", "int64")])
        b1 = RecordBatch(schema, {"id": [1, 2], "lo": [0, 3], "hi": [5, 3]})
        b2 = RecordBatch(schema, {"id": [3], "lo": [None], "hi": [8]})
        t = Table(schema, [b1, b2])
        self.assertEqual(t.num_rows, 3)
        self.assertEqual(t.value("lo", 2), None)
        ref = iv_table([(9, 0, 100)])
        res = interval_interval_join(t, "id", "lo", "hi",
                                     ref, "id", "lo", "hi")
        self.assertEqual(pairs(res), [(1, 9, 5)])
        self.assertEqual(res.left_stats.empty_interval_rows, 1)
        self.assertEqual(res.left_stats.null_endpoint_rows, 1)
        # 排序按全局行号
        self.assertEqual([m.left_row for m in res.matches], [0])


class IndexTest(unittest.TestCase):
    def test_index_stab_boundary(self):
        idx = IntervalIndex([Interval(0, 5, 0, "a"), Interval(5, 8, 1, "b")])
        got = {h.source_id for h in idx.stab(5)}
        self.assertEqual(got, {"b"})
        self.assertEqual({h.source_id for h in idx.stab(4)}, {"a"})
        self.assertEqual({h.source_id for h in idx.stab(8)}, set())

    def test_index_overlaps_adjacent(self):
        idx = IntervalIndex([Interval(0, 5, 0), Interval(5, 8, 1),
                             Interval(2, 6, 2)])
        self.assertEqual({h.payload for h in idx.overlaps(5, 9)}, {1, 2})
        # [0,5) 与 [0,5) 正长度相交；与 [2,6) 交长 3；与 [5,8) 不相交
        self.assertEqual({h.payload for h in idx.overlaps(0, 5)}, {0, 2})
        self.assertEqual({h.payload for h in idx.overlaps(8, 9)}, set())
        self.assertEqual(list(idx.overlaps(3, 3)), [])


class BruteForceEquivalenceTest(unittest.TestCase):
    """随机数据：索引连接、扫描线与暴力笛卡尔三方对账。"""

    def brute_iv(self, L, R):
        out = []
        for lid, a, b in L:
            for rid, c, d in R:
                if a < b and c < d and max(a, c) < min(b, d):
                    out.append((lid, rid, min(b, d) - max(a, c)))
        return sorted(out)

    def brute_pt(self, P, R):
        out = []
        for pid, p in P:
            for rid, c, d in R:
                if c < d and c <= p < d:
                    out.append((pid, rid, 0))
        return sorted(out)

    def test_random_interval_join(self):
        rng = random.Random(20260924)
        for trial in range(60):
            L, R = [], []
            for i in range(rng.randint(0, 12)):
                a, b = rng.randint(-6, 6), rng.randint(-6, 10)
                L.append((i, a, b))
            for j in range(rng.randint(0, 12)):
                a, b = rng.randint(-6, 6), rng.randint(-6, 10)
                R.append((100 + j, a, b))
            lt, rt = iv_table(L), iv_table(R)
            expect = self.brute_iv(L, R)
            r1 = sorted(pairs(interval_interval_join(
                lt, "id", "lo", "hi", rt, "id", "lo", "hi")))
            r2 = sorted(pairs(sweep_interval_join(
                lt, "id", "lo", "hi", rt, "id", "lo", "hi")))
            self.assertEqual(r1, expect, f"trial {trial} L={L} R={R}")
            self.assertEqual(r2, expect, f"trial {trial} sweep")

    def test_random_point_join(self):
        rng = random.Random(4242)
        for trial in range(60):
            P = [(i, rng.randint(-7, 11)) for i in range(rng.randint(0, 10))]
            R = []
            for j in range(rng.randint(0, 10)):
                a, b = rng.randint(-6, 6), rng.randint(-6, 10)
                R.append((100 + j, a, b))
            pt, rt = pt_table([p for _, p in P]), iv_table(R)
            expect = self.brute_pt(P, R)
            r1 = sorted(pairs(point_interval_join(
                pt, "id", "p", rt, "id", "lo", "hi")))
            r2 = sorted(pairs(sweep_point_join(
                pt, "id", "p", rt, "id", "lo", "hi")))
            self.assertEqual(r1, expect, f"trial {trial} P={P} R={R}")
            self.assertEqual(r2, expect)


class StatsTest(unittest.TestCase):
    def test_naive_vs_examined(self):
        # R 侧 1000 条互不相交的小区间；查询落在空隙中时索引零枚举，
        # 朴素笛卡尔却要比较 1000 对。
        right = iv_table([(i, 10 * i, 10 * i + 2) for i in range(1000)])
        miss = iv_table([(1, -100, -90), (2, 5005, 5006)])
        res = interval_interval_join(miss, "id", "lo", "hi",
                                     right, "id", "lo", "hi")
        self.assertEqual(res.naive_pairs, 2000)
        self.assertEqual(res.examined_pairs, 0)
        self.assertEqual(res.matches, [])

        hit = iv_table([(3, 31, 42)])  # 与 [30,32) [40,42) 正长度相交 2 条
        res2 = interval_interval_join(hit, "id", "lo", "hi",
                                      right, "id", "lo", "hi")
        self.assertEqual(res2.naive_pairs, 1000)
        self.assertLessEqual(res2.examined_pairs, 40)  # 只沿 O(log n) 分支
        self.assertEqual(len(res2.matches), 2)


if __name__ == "__main__":
    unittest.main(verbosity=2)
