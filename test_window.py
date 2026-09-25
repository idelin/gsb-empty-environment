import math
import unittest

from window import (
    Batch,
    Column,
    CURRENT_ROW,
    FOLLOWING,
    Frame,
    OrderKey,
    PRECEDING,
    RANK,
    SUM,
    UNBOUNDED_FOLLOWING,
    UNBOUNDED_PRECEDING,
    WindowEngine,
    run,
)

COLS = [Column("g", "str"), Column("k", "int"), Column("v", "int")]
FCOLS = [Column("g", "str"), Column("k", "float"), Column("v", "int")]


def rows_of(pairs, g="a"):
    return [{"g": g, "k": k, "v": v} for k, v in pairs]


class TestRank(unittest.TestCase):
    def test_peer_group_rank_1_1_3(self):
        out = run([Batch(COLS, rows_of([(1, 10), (1, 20), (2, 30)]))],
                  ["g"], [OrderKey("k")], [RANK()])
        self.assertEqual([r["rank"] for r in out], [1, 1, 3])

    def test_rank_per_partition(self):
        rows = rows_of([(1, 1), (2, 2)], "a") + rows_of([(5, 3), (5, 4)], "b")
        out = run([Batch(COLS, rows)], ["g"], [OrderKey("k")], [RANK()])
        self.assertEqual([r["rank"] for r in out], [1, 2, 1, 1])

    def test_desc(self):
        out = run([Batch(COLS, rows_of([(1, 10), (2, 20), (3, 30)]))],
                  ["g"], [OrderKey("k", desc=True)], [RANK()])
        self.assertEqual([r["rank"] for r in out], [3, 2, 1])


class TestRowsVsRange(unittest.TestCase):
    def setUp(self):
        self.rows = rows_of([(1, 10), (1, 20), (2, 30)])
        self.order = [OrderKey("k")]

    def test_rows_current_row_only_self(self):
        m = SUM("v", Frame("rows", CURRENT_ROW, CURRENT_ROW))
        out = run([Batch(COLS, self.rows)], ["g"], self.order, [m])
        self.assertEqual([r["sum_v"] for r in out], [10, 20, 30])

    def test_range_current_row_includes_peers(self):
        m = SUM("v", Frame("range", CURRENT_ROW, CURRENT_ROW))
        out = run([Batch(COLS, self.rows)], ["g"], self.order, [m])
        self.assertEqual([r["sum_v"] for r in out], [30, 30, 30])

    def test_cumulative_rows_vs_range(self):
        ms = [SUM("v", Frame("rows"), "rows_cum"),
              SUM("v", Frame("range"), "range_cum")]
        out = run([Batch(COLS, self.rows)], ["g"], self.order, ms)
        self.assertEqual([r["rows_cum"] for r in out], [10, 30, 60])
        self.assertEqual([r["range_cum"] for r in out], [30, 30, 60])

    def test_rows_offsets(self):
        m = SUM("v", Frame("rows", PRECEDING(1), FOLLOWING(1)))
        rows = rows_of([(1, 1), (2, 2), (3, 3), (4, 4)])
        out = run([Batch(COLS, rows)], ["g"], self.order, [m])
        self.assertEqual([r["sum_v"] for r in out], [3, 6, 9, 7])

    def test_rows_unbounded_following(self):
        m = SUM("v", Frame("rows", CURRENT_ROW, UNBOUNDED_FOLLOWING))
        out = run([Batch(COLS, self.rows)], ["g"], self.order, [m])
        self.assertEqual([r["sum_v"] for r in out], [60, 50, 30])

    def test_range_offsets(self):
        m = SUM("v", Frame("range", PRECEDING(1), FOLLOWING(1)))
        rows = rows_of([(1, 1), (2, 1), (3, 1), (10, 1)])
        out = run([Batch(COLS, rows)], ["g"], self.order, [m])
        self.assertEqual([r["sum_v"] for r in out], [2, 3, 2, 1])


class TestNullAndNaN(unittest.TestCase):
    def test_nulls_first_explicit(self):
        rows = rows_of([(None, 1), (1, 2), (2, 3)])
        out = run([Batch(COLS, rows)], ["g"],
                  [OrderKey("k", nulls_first=True)], [RANK()])
        self.assertEqual([r["rank"] for r in out], [1, 2, 3])

    def test_nulls_last_explicit(self):
        rows = rows_of([(None, 1), (1, 2), (2, 3)])
        out = run([Batch(COLS, rows)], ["g"],
                  [OrderKey("k", nulls_first=False)], [RANK()])
        self.assertEqual([r["rank"] for r in out], [3, 1, 2])

    def test_nulls_position_independent_of_desc(self):
        rows = rows_of([(None, 1), (1, 2), (2, 3)])
        out = run([Batch(COLS, rows)], ["g"],
                  [OrderKey("k", desc=True, nulls_first=True)], [RANK()])
        self.assertEqual([r["rank"] for r in out], [1, 3, 2])

    def test_nan_distinct_from_null(self):
        nan = float("nan")
        rows = [{"g": "a", "k": 1.0, "v": 1},
                {"g": "a", "k": nan, "v": 2},
                {"g": "a", "k": None, "v": 3},
                {"g": "a", "k": nan, "v": 4}]
        # 升序 + NULLS LAST：1.0 < NaN = NaN < NULL
        out = run([Batch(FCOLS, rows)], ["g"],
                  [OrderKey("k", nulls_first=False)], [RANK()])
        self.assertEqual([r["rank"] for r in out], [1, 2, 4, 2])

    def test_sum_ignores_null_all_null_gives_null(self):
        rows = rows_of([(1, None), (2, 5), (3, None)])
        m = SUM("v", Frame("rows"))
        out = run([Batch(COLS, rows)], ["g"], [OrderKey("k")], [m])
        self.assertEqual([r["sum_v"] for r in out], [None, 5, 5])

    def test_nan_propagates_in_sum_unlike_null(self):
        nan = float("nan")
        rows = [{"g": "a", "k": 1, "v": 1.0},
                {"g": "a", "k": 2, "v": nan},
                {"g": "a", "k": 3, "v": 2.0}]
        fcols = [Column("g", "str"), Column("k", "int"), Column("v", "float")]
        m = SUM("v", Frame("rows"))
        out = run([Batch(fcols, rows)], ["g"], [OrderKey("k")], [m])
        self.assertEqual(out[0]["sum_v"], 1.0)
        self.assertTrue(math.isnan(out[1]["sum_v"]))
        self.assertTrue(math.isnan(out[2]["sum_v"]))


class TestExactInt(unittest.TestCase):
    def test_big_int_exact(self):
        big1, big2 = 2**63, 2**63 + 1
        rows = rows_of([(1, big1), (2, big2)])
        m = SUM("v", Frame("rows"))
        out = run([Batch(COLS, rows)], ["g"], [OrderKey("k")], [m])
        self.assertEqual(out[-1]["sum_v"], big1 + big2)
        self.assertIsInstance(out[-1]["sum_v"], int)


class TestChunking(unittest.TestCase):
    def test_chunk_boundary_does_not_truncate_partition(self):
        cols = COLS
        order = [OrderKey("k")]
        measures = [RANK(), SUM("v", Frame("range"), "rcum"),
                    SUM("v", Frame("rows"), "cum")]
        all_rows = (rows_of([(1, 10), (1, 20), (2, 30)], "a")
                    + rows_of([(1, 5), (3, 7)], "b"))
        whole = run([Batch(cols, all_rows)], ["g"], order, measures)

        # 分区 a 被切到两个分块，分区 b 也被切到两个分块
        eng = WindowEngine(["g"], order, measures)
        chunked = []
        chunked += eng.add_batch(Batch(cols, all_rows[:2]))   # a 的前两行
        chunked += eng.add_batch(Batch(cols, all_rows[2:4]))  # a 余下 + b 首行
        chunked += eng.add_batch(Batch(cols, all_rows[4:]))   # b 余下
        chunked += eng.finish()
        self.assertEqual(chunked, whole)
        # 分区 a 的 range 累计和确实跨分块聚合（10,30,60 而非被截断）
        self.assertEqual([r["rcum"] for r in chunked[:3]], [30, 30, 60])

    def test_output_rows_follow_input_order(self):
        rows = rows_of([(2, 1), (1, 2), (3, 3)], "a") + rows_of([(9, 4)], "b")
        out = run([Batch(COLS, rows)], ["g"], [OrderKey("k")], [RANK()])
        self.assertEqual([(r["g"], r["k"], r["rank"]) for r in out],
                         [("a", 2, 2), ("a", 1, 1), ("a", 3, 3), ("b", 9, 1)])


class TestBatchValidation(unittest.TestCase):
    def test_type_mismatch_raises(self):
        with self.assertRaises(TypeError):
            Batch(COLS, [{"g": "a", "k": "not-an-int", "v": 1}])

    def test_unknown_column_raises(self):
        with self.assertRaises(ValueError):
            Batch(COLS, [{"g": "a", "k": 1, "v": 1, "x": 0}])

    def test_null_allowed_for_any_type(self):
        b = Batch(COLS, [{"g": None, "k": None, "v": None}])
        self.assertEqual(len(b.rows), 1)


if __name__ == "__main__":
    unittest.main()
