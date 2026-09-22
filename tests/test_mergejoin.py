"""mergejoin 的 unittest 测试套件。"""

from __future__ import annotations

import os
import sys
import tempfile
import threading
import unittest
from collections.abc import Iterator

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from mergejoin import (  # noqa: E402
    BoundedSpool,
    CancellationToken,
    JoinCancelled,
    OrderViolationError,
    merge_join,
)
from mergejoin.relations import (  # noqa: E402
    BIG_GROUP_SIZE,
    LEFT_ROWS,
    RIGHT_ROWS,
    big_left_rows,
    big_right_rows,
)


class TestInnerJoin(unittest.TestCase):
    def test_basic_matches(self):
        result = list(merge_join([(1, "a"), (2, "b")], [(1, "x"), (2, "y")]))
        self.assertEqual(result, [
            ((1, "a"), (1, "x")),
            ((2, "b"), (2, "y")),
        ])

    def test_2x3_duplicate_group_yields_six(self):
        left = [(2, "a"), (2, "b")]
        right = [(2, "x"), (2, "y"), (2, "z")]
        result = list(merge_join(left, right, how="inner"))
        self.assertEqual(len(result), 6)
        self.assertEqual(result, [
            ((2, "a"), (2, "x")),
            ((2, "a"), (2, "y")),
            ((2, "a"), (2, "z")),
            ((2, "b"), (2, "x")),
            ((2, "b"), (2, "y")),
            ((2, "b"), (2, "z")),
        ])

    def test_multiset_keeps_identical_duplicates(self):
        left = [(1, "d"), (1, "d")]
        right = [(1, "d"), (1, "d"), (1, "d")]
        result = list(merge_join(left, right))
        self.assertEqual(len(result), 6)
        self.assertEqual(result, [(((1, "d"), (1, "d")))] * 6)

    def test_synthetic_relation_inner_rowcount(self):
        result = list(merge_join(LEFT_ROWS, RIGHT_ROWS, how="inner"))
        # key=1: 1, key=2: 2*3=6, key=4: 1 -> 8；NULL、key=3、key=5 不匹配。
        self.assertEqual(len(result), 8)
        self.assertEqual(sum(1 for lr, rr in result if lr[0] == 2), 6)

    def test_empty_either_side(self):
        self.assertEqual(list(merge_join([], RIGHT_ROWS)), [])
        self.assertEqual(list(merge_join(LEFT_ROWS, [])), [])
        self.assertEqual(list(merge_join([], [])), [])

    def test_repeated_iteration_impossible_is_ok_with_lists(self):
        # 同一列表数据源可以多次连接。
        left = [(1, "a")]
        first = list(merge_join(left, [(1, "x")]))
        second = list(merge_join(left, [(1, "y")]))
        self.assertEqual(len(first), 1)
        self.assertEqual(len(second), 1)


class TestLeftOuterJoin(unittest.TestCase):
    def test_unmatched_left_padded_with_nulls(self):
        right_arity = 2
        result = list(merge_join(
            [(1, "a"), (2, "b")], [(1, "x")],
            how="left", right_arity=right_arity,
        ))
        self.assertEqual(result, [
            ((1, "a"), (1, "x")),
            ((2, "b"), (None, None)),
        ])

    def test_synthetic_relation_left_rowcount(self):
        result = list(merge_join(LEFT_ROWS, RIGHT_ROWS, how="left"))
        # 内连接 8 行 + 左 NULL 1 行 + 左独有 key=5 1 行 = 10。
        self.assertEqual(len(result), 10)
        null_rows = [(lr, rr) for lr, rr in result if lr[0] is None]
        self.assertEqual(null_rows, [((None, "L_null"), (None, None))])
        key5 = [(lr, rr) for lr, rr in result if lr[0] == 5]
        self.assertEqual(key5, [((5, "L_5"), (None, None))])

    def test_left_null_duplicate_group_all_padded(self):
        left = [(None, "n1"), (None, "n2"), (1, "a")]
        right = [(None, "rn"), (1, "x")]
        result = list(merge_join(left, right, how="left"))
        self.assertEqual(result, [
            ((None, "n1"), (None, None)),
            ((None, "n2"), (None, None)),
            ((1, "a"), (1, "x")),
        ])

    def test_right_only_key_dropped_in_left_join(self):
        result = list(merge_join([(1, "a")], [(1, "x"), (2, "y")], how="left"))
        self.assertEqual(result, [((1, "a"), (1, "x"))])

    def test_right_arity_inferred(self):
        result = list(merge_join([(1, "a"), (2, "b")], [(1, "x", "z")], how="left"))
        self.assertEqual(result, [
            ((1, "a"), (1, "x", "z")),
            ((2, "b"), (None, None, None)),
        ])


class TestNullSemantics(unittest.TestCase):
    def test_nulls_never_match_inner(self):
        result = list(merge_join([(None, "a"), (1, "b")], [(None, "x"), (1, "y")]))
        self.assertEqual(result, [((1, "b"), (1, "y"))])

    def test_null_sorts_first_and_groups(self):
        left = [(None, "a2"), (None, "a1"), (1, "b")]
        right = [(1, "y")]
        result = list(merge_join(left, right, how="left"))
        self.assertEqual([lr for lr, _ in result], [(None, "a2"), (None, "a1"), (1, "b")])


class TestSpill(unittest.TestCase):
    def test_both_groups_spill_still_completes(self):
        result = list(merge_join(
            big_left_rows(), big_right_rows(),
            how="inner", memory_limit=8,
        ))
        self.assertEqual(len(result), BIG_GROUP_SIZE * BIG_GROUP_SIZE + 2)
        self.assertEqual(sum(1 for lr, rr in result if lr[0] == 2),
                         BIG_GROUP_SIZE * BIG_GROUP_SIZE)

    def test_one_sided_group_spill_left_join(self):
        right = [(7, "R_7_only")]
        result = list(merge_join(
            __import__("mergejoin.relations", fromlist=["one_sided_big_left"])
            .one_sided_big_left(),
            right, how="left", memory_limit=8,
        ))
        self.assertEqual(len(result), BIG_GROUP_SIZE)
        self.assertTrue(all(rr == (7, "R_7_only") for _, rr in result))

    def test_spill_files_removed_after_consumption(self):
        import mergejoin.join as join_mod

        created: list[str] = []

        class TrackingSpool(BoundedSpool):
            def append(self, row):
                super().append(row)
                if self.path is not None and (not created or created[-1] != self.path):
                    if self.path not in created:
                        created.append(self.path)

        tmp_dir = tempfile.mkdtemp()
        original = join_mod.BoundedSpool
        join_mod.BoundedSpool = TrackingSpool
        try:
            list(merge_join(
                big_left_rows(), big_right_rows(),
                memory_limit=8, tmp_dir=tmp_dir,
            ))
        finally:
            join_mod.BoundedSpool = original
        self.assertGreaterEqual(len(created), 2, "两侧键组都应溢写")
        for path in created:
            self.assertFalse(os.path.exists(path), f"{path} 未被清理")
        self.assertEqual(os.listdir(tmp_dir), [])

    def test_spool_memory_and_disk_iteration(self):
        spool = BoundedSpool(memory_limit=3)
        for i in range(10):
            spool.append((i, f"v{i}"))
        spool.finish_write()
        self.assertTrue(spool.spilled)
        self.assertEqual(spool.count, 10)
        first_pass = list(spool.rows())
        second_pass = list(spool.rows())
        self.assertEqual(first_pass, [(i, f"v{i}") for i in range(10)])
        self.assertEqual(first_pass, second_pass)
        self.assertTrue(os.path.exists(spool.path))
        spool.close()
        self.assertFalse(os.path.exists(spool.path))

    def test_spool_holds_in_memory_under_limit(self):
        spool = BoundedSpool(memory_limit=10)
        for i in range(3):
            spool.append((i,))
        spool.finish_write()
        self.assertFalse(spool.spilled)
        self.assertEqual(list(spool.rows()), [(0,), (1,), (2,)])
        spool.close()

    def test_group_over_memory_with_duplicates_multiset(self):
        def left() -> Iterator[tuple]:
            yield (1, "x")
            for i in range(50):
                yield (2, f"l{i}")
            yield (3, "z")

        def right() -> Iterator[tuple]:
            for i in range(40):
                yield (2, f"r{i}")

        result = list(merge_join(left(), right(), memory_limit=8))
        self.assertEqual(len(result), 50 * 40)


class TestOrderViolation(unittest.TestCase):
    def test_left_out_of_order_raises(self):
        with self.assertRaises(OrderViolationError):
            list(merge_join([(1, "a"), (3, "c"), (2, "b")], [(1, "x")]))

    def test_right_out_of_order_raises(self):
        with self.assertRaises(OrderViolationError):
            list(merge_join([(1, "a")], [(2, "y"), (1, "x")]))

    def test_order_violation_seen_during_drain(self):
        # 首个键组读完后才发现右侧回退。
        right = [(1, "x1"), (3, "y"), (2, "z")]
        with self.assertRaises(OrderViolationError):
            list(merge_join([(1, "a")], right))

    def test_nondecreasing_equal_and_increasing_ok(self):
        result = list(merge_join([(1, "a"), (1, "b"), (2, "c")],
                                 [(1, "x"), (2, "y"), (2, "z")]))
        # key=1: 2*1=2，key=2: 1*2=2。
        self.assertEqual(len(result), 4)

    def test_order_violation_before_any_output(self):
        consumed = 0
        with self.assertRaises(OrderViolationError):
            for _ in merge_join([(2, "a"), (1, "b")], [(1, "x")]):
                consumed += 1
        self.assertEqual(consumed, 0)


class TestCancellation(unittest.TestCase):
    def test_precancelled_token_raises(self):
        token = CancellationToken()
        token.cancel()
        with self.assertRaises(JoinCancelled):
            list(merge_join(LEFT_ROWS, RIGHT_ROWS, token=token))

    def test_cancel_midway_from_thread(self):
        token = CancellationToken()

        def _cancel() -> None:
            token.cancel()

        thread = threading.Thread(target=_cancel)
        seen = 0
        with self.assertRaises(JoinCancelled):
            gen = merge_join(big_left_rows(), big_right_rows(),
                             how="inner", memory_limit=8, token=token)
            thread.start()
            for _ in gen:
                seen += 1
        thread.join()
        self.assertGreaterEqual(seen, 0)

    def test_cancel_cleans_spool_files(self):
        class CancelAfter(CancellationToken):
            def __init__(self, limit):
                super().__init__()
                self.limit = limit
                self.checks = 0

            def raise_if_cancelled(self):
                self.checks += 1
                if self.checks >= self.limit:
                    self.cancel()
                super().raise_if_cancelled()

        tmp_dir = tempfile.mkdtemp()
        # 限制足够大：两侧 key=2 组（各 1000 行）已经溢写，但尚未全部配对完。
        token = CancelAfter(3000)
        with self.assertRaises(JoinCancelled):
            list(merge_join(big_left_rows(), big_right_rows(),
                            memory_limit=8, tmp_dir=tmp_dir, token=token))
        self.assertEqual(os.listdir(tmp_dir), [])


class TestArguments(unittest.TestCase):
    def test_bad_how_raises_eagerly(self):
        with self.assertRaises(ValueError):
            merge_join(LEFT_ROWS, RIGHT_ROWS, how="outer")

    def test_bad_memory_limit(self):
        with self.assertRaises(ValueError):
            BoundedSpool(memory_limit=-1)

    def test_non_tuple_row_raises(self):
        with self.assertRaises(TypeError):
            list(merge_join(iter([[1, "a"]]), [(1, "x")]))


if __name__ == "__main__":
    unittest.main(verbosity=2)
