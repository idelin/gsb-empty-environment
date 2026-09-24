import math
import unittest

from window import (
    CURRENT_ROW,
    FOLLOWING,
    PRECEDING,
    UNBOUNDED_FOLLOWING,
    UNBOUNDED_PRECEDING,
    Frame,
    OrderKey,
    SchemaError,
    WindowEngine,
    WindowSpecError,
)

NAN = float("nan")


def make_engine(frame, order_by=None, schema=None, partition_by=("g",), sum_column="v"):
    return WindowEngine(
        schema=schema or [("g", "str"), ("k", "int"), ("v", "int")],
        partition_by=list(partition_by),
        order_by=list(order_by) if order_by else [OrderKey("k")],
        sum_column=sum_column,
        frame=frame,
    )


def sums(result):
    return [r["sum"] for r in result]


def ranks(result):
    return [r["rank"] for r in result]


class RankAndPeerTest(unittest.TestCase):
    def test_rank_with_peers_1_1_3(self):
        engine = make_engine(Frame("rows", CURRENT_ROW, CURRENT_ROW))
        engine.add_batch([("a", 1, 10), ("a", 1, 20), ("a", 2, 30)])
        result = engine.compute()
        self.assertEqual(ranks(result), [1, 1, 3])

    def test_rows_current_row_contains_only_self(self):
        engine = make_engine(Frame("rows", CURRENT_ROW, CURRENT_ROW))
        engine.add_batch([("a", 1, 10), ("a", 1, 20), ("a", 2, 30)])
        self.assertEqual(sums(engine.compute()), [10, 20, 30])

    def test_range_current_row_includes_all_peers(self):
        engine = make_engine(Frame("range", UNBOUNDED_PRECEDING, CURRENT_ROW))
        engine.add_batch([("a", 1, 10), ("a", 1, 20), ("a", 2, 30)])
        # 键 1 的两行互为 peer：RANGE 到 CURRENT ROW 时两行都看到 10+20
        self.assertEqual(sums(engine.compute()), [30, 30, 60])
        self.assertEqual(ranks(engine.compute()), [1, 1, 3])

    def test_rank_resets_per_partition(self):
        engine = make_engine(Frame("rows", CURRENT_ROW, CURRENT_ROW))
        engine.add_batch([("a", 1, 1), ("b", 5, 2), ("a", 1, 3), ("b", 7, 4)])
        self.assertEqual(ranks(engine.compute()), [1, 1, 1, 2])

    def test_unsorted_input_is_sorted_within_partition(self):
        engine = make_engine(Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW))
        engine.add_batch([("a", 3, 30), ("a", 1, 10), ("a", 2, 20)])
        self.assertEqual(sums(engine.compute()), [60, 10, 30])


class ChunkBoundaryTest(unittest.TestCase):
    def test_partition_spans_multiple_batches(self):
        engine = make_engine(Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW))
        engine.add_batch([("a", 1, 10), ("b", 1, 100)])
        engine.add_batch([("a", 2, 20)])
        engine.add_batch([("a", 3, 30), ("b", 2, 200)])
        result = engine.compute()
        # 分区 a 跨三个批次：累计和不被批次边界截断
        self.assertEqual(sums(result), [10, 100, 30, 60, 300])
        self.assertEqual(ranks(result), [1, 1, 2, 3, 2])

    def test_empty_batch_and_empty_input(self):
        engine = make_engine(Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW))
        engine.add_batch([])
        self.assertEqual(engine.compute(), [])


class NullHandlingTest(unittest.TestCase):
    def test_nulls_first_explicit(self):
        engine = make_engine(
            Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW),
            order_by=[OrderKey("k", nulls_first=True)],
        )
        engine.add_batch([("a", 2, 20), ("a", None, 5), ("a", 1, 10)])
        # NULLS FIRST 后顺序为 None,1,2，累计和 5,15,35（按输入顺序回填）
        self.assertEqual(sums(engine.compute()), [35, 5, 15])

    def test_nulls_last_default_for_asc(self):
        engine = make_engine(Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW))
        engine.add_batch([("a", None, 5), ("a", 1, 10), ("a", 2, 20)])
        self.assertEqual(sums(engine.compute()), [35, 10, 30])

    def test_nulls_first_default_for_desc(self):
        engine = make_engine(
            Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW),
            order_by=[OrderKey("k", descending=True)],
        )
        engine.add_batch([("a", 1, 10), ("a", None, 5), ("a", 2, 20)])
        self.assertEqual(sums(engine.compute()), [35, 5, 25])

    def test_null_value_skipped_in_sum(self):
        engine = make_engine(Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW))
        engine.add_batch([("a", 1, 10), ("a", 2, None), ("a", 3, 30)])
        self.assertEqual(sums(engine.compute()), [10, 10, 40])

    def test_all_null_frame_gives_none(self):
        engine = make_engine(Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW))
        engine.add_batch([("a", 1, None), ("a", 2, None)])
        self.assertEqual(sums(engine.compute()), [None, None])

    def test_null_keys_are_peers(self):
        engine = make_engine(Frame("range", CURRENT_ROW, CURRENT_ROW))
        engine.add_batch([("a", None, 1), ("a", None, 2), ("a", 1, 3)])
        result = engine.compute()
        self.assertEqual(sums(result), [3, 3, 3])
        # 升序 NULLS LAST：键 1 排第一，两个 NULL 键互为 peer 并列第二
        self.assertEqual(ranks(result), [2, 2, 1])


class NanHandlingTest(unittest.TestCase):
    def setUp(self):
        self.schema = [("g", "str"), ("k", "float"), ("v", "float")]

    def test_nan_is_not_null_and_sorts_after_numbers(self):
        engine = make_engine(
            Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW), schema=self.schema
        )
        engine.add_batch(
            [("a", NAN, 20.0), ("a", None, 30.0), ("a", 1.0, 10.0), ("a", 2.0, 40.0)]
        )
        # 升序 NULLS LAST，NaN 排在数值之后、NULL 之前：1.0, 2.0, NaN, NULL
        # NaN 只是排序键，不是 NULL，其 v 正常参与求和
        self.assertEqual(sums(engine.compute()), [70.0, 100.0, 10.0, 50.0])

    def test_nan_value_propagates_into_sum(self):
        engine = make_engine(
            Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW), schema=self.schema
        )
        engine.add_batch([("a", 1.0, 10.0), ("a", 2.0, NAN), ("a", 3.0, 30.0)])
        got = sums(engine.compute())
        self.assertEqual(got[0], 10.0)
        self.assertTrue(math.isnan(got[1]))
        self.assertTrue(math.isnan(got[2]))

    def test_nan_keys_are_peers_and_rank_together(self):
        engine = make_engine(Frame("rows", CURRENT_ROW, CURRENT_ROW), schema=self.schema)
        engine.add_batch([("a", 1.0, 1.0), ("a", NAN, 2.0), ("a", NAN, 3.0)])
        self.assertEqual(ranks(engine.compute()), [1, 2, 2])

    def test_null_still_skipped_when_nan_present(self):
        engine = make_engine(
            Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW), schema=self.schema
        )
        engine.add_batch([("a", 1.0, None), ("a", 2.0, 5.0)])
        self.assertEqual(sums(engine.compute()), [None, 5.0])


class ExactIntegerTest(unittest.TestCase):
    def test_big_integer_sum_is_exact(self):
        big = 2**80 + 12345
        engine = make_engine(Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW))
        engine.add_batch([("a", 1, big), ("a", 2, big), ("a", 3, 7)])
        result = engine.compute()
        self.assertEqual(sums(result), [big, 2 * big, 2 * big + 7])
        for row in result:
            self.assertIsInstance(row["sum"], int)


class RowsFrameTest(unittest.TestCase):
    def test_rows_one_preceding_to_current(self):
        engine = make_engine(Frame("rows", PRECEDING(1), CURRENT_ROW))
        engine.add_batch([("a", k, k * 10) for k in (1, 2, 3, 4, 5)])
        self.assertEqual(sums(engine.compute()), [10, 30, 50, 70, 90])

    def test_rows_between_offsets(self):
        engine = make_engine(Frame("rows", PRECEDING(1), FOLLOWING(1)))
        engine.add_batch([("a", k, k) for k in (1, 2, 3, 4)])
        self.assertEqual(sums(engine.compute()), [3, 6, 9, 7])

    def test_rows_unbounded_following(self):
        engine = make_engine(Frame("rows", CURRENT_ROW, UNBOUNDED_FOLLOWING))
        engine.add_batch([("a", k, k) for k in (1, 2, 3)])
        self.assertEqual(sums(engine.compute()), [6, 5, 3])

    def test_empty_frame_gives_none(self):
        engine = make_engine(Frame("rows", PRECEDING(1), PRECEDING(1)))
        engine.add_batch([("a", 1, 10), ("a", 2, 20), ("a", 3, 30)])
        self.assertEqual(sums(engine.compute()), [None, 10, 20])


class RangeFrameTest(unittest.TestCase):
    def test_range_numeric_offset(self):
        engine = make_engine(Frame("range", PRECEDING(1), CURRENT_ROW))
        engine.add_batch([("a", 1, 1), ("a", 2, 1), ("a", 4, 1), ("a", 5, 1)])
        # 键值窗口 [k-1, k]：1->{1}, 2->{1,2}, 4->{4}, 5->{4,5}
        self.assertEqual(sums(engine.compute()), [1, 2, 1, 2])

    def test_range_offset_includes_peer_boundary(self):
        engine = make_engine(Frame("range", CURRENT_ROW, FOLLOWING(2)))
        engine.add_batch([("a", 1, 1), ("a", 2, 1), ("a", 2, 1), ("a", 3, 1)])
        # 键 1 -> [1,3] 全部 4 行；键 2 的 peer 组 -> [2,4] 即键 2,2,3
        self.assertEqual(sums(engine.compute()), [4, 3, 3, 1])

    def test_range_offset_null_key_only_sees_peers(self):
        engine = make_engine(Frame("range", PRECEDING(1), FOLLOWING(1)))
        engine.add_batch([("a", None, 5), ("a", None, 7), ("a", 1, 10)])
        self.assertEqual(sums(engine.compute()), [12, 12, 10])


class ValidationTest(unittest.TestCase):
    def test_int_column_rejects_float(self):
        engine = make_engine(Frame("rows", CURRENT_ROW, CURRENT_ROW))
        with self.assertRaises(SchemaError):
            engine.add_batch([("a", 1, 1.5)])

    def test_int_column_rejects_nan(self):
        engine = make_engine(Frame("rows", CURRENT_ROW, CURRENT_ROW))
        with self.assertRaises(SchemaError):
            engine.add_batch([("a", 1, NAN)])

    def test_int_column_rejects_bool(self):
        engine = make_engine(Frame("rows", CURRENT_ROW, CURRENT_ROW))
        with self.assertRaises(SchemaError):
            engine.add_batch([("a", 1, True)])

    def test_row_width_mismatch(self):
        engine = make_engine(Frame("rows", CURRENT_ROW, CURRENT_ROW))
        with self.assertRaises(SchemaError):
            engine.add_batch([("a", 1)])

    def test_frame_start_after_end_rejected(self):
        with self.assertRaises(WindowSpecError):
            Frame("rows", CURRENT_ROW, PRECEDING(1))

    def test_range_offset_requires_single_numeric_asc_key(self):
        with self.assertRaises(WindowSpecError):
            make_engine(
                Frame("range", PRECEDING(1), CURRENT_ROW),
                order_by=[OrderKey("k"), OrderKey("v")],
            )
        with self.assertRaises(WindowSpecError):
            make_engine(
                Frame("range", PRECEDING(1), CURRENT_ROW),
                order_by=[OrderKey("k", descending=True)],
            )

    def test_unknown_column_rejected(self):
        with self.assertRaises(WindowSpecError):
            make_engine(
                Frame("rows", CURRENT_ROW, CURRENT_ROW),
                order_by=[OrderKey("nope")],
            )


if __name__ == "__main__":
    unittest.main()
