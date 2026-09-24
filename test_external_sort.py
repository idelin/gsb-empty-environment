"""Unit tests for external_sort (stdlib unittest, Python 3.11+)."""

import math
import os
import tempfile
import unittest
from unittest import mock

from external_sort import (
    Batch,
    Column,
    ColumnType,
    ExternalSorter,
    SortCancelled,
    SortError,
    SortKey,
)

NAN = float("nan")

SCHEMA = (
    Column("id", ColumnType.INT),
    Column("score", ColumnType.FLOAT),
    Column("name", ColumnType.STRING),
)


def make_sorter(tmp, keys, budget=1 << 20):
    return ExternalSorter(SCHEMA, keys, mem_budget=budget, temp_dir=tmp)


class SortSemanticsTest(unittest.TestCase):
    def setUp(self):
        self._tmp_ctx = tempfile.TemporaryDirectory()
        self.tmp = self._tmp_ctx.name

    def tearDown(self):
        self._tmp_ctx.cleanup()

    def test_mixed_asc_desc_and_null_position(self):
        # score DESC (nulls last), then name ASC (nulls first)
        keys = [
            SortKey("score", ascending=False, nulls_first=False),
            SortKey("name", ascending=True, nulls_first=True),
        ]
        sorter = make_sorter(self.tmp, keys)
        sorter.add_batch(Batch(SCHEMA, [
            (1, 2.0, "b"),
            (2, None, "z"),
            (3, 3.0, None),
            (4, 2.0, "a"),
            (5, 3.0, "a"),
            (6, None, "a"),
        ]))
        rows = sorter.sort_to_list()
        self.assertEqual(
            rows,
            [
                (3, 3.0, None),   # score 3.0, name null first
                (5, 3.0, "a"),
                (4, 2.0, "a"),    # score 2.0, name asc
                (1, 2.0, "b"),
                (6, None, "a"),   # score null last, name asc
                (2, None, "z"),
            ],
        )

    def test_nulls_first_with_desc(self):
        keys = [SortKey("score", ascending=False, nulls_first=True)]
        sorter = make_sorter(self.tmp, keys)
        sorter.add_batch(Batch(SCHEMA, [(1, 1.0, "x"), (2, None, "y"), (3, 2.0, "z")]))
        rows = sorter.sort_to_list()
        self.assertEqual([r[0] for r in rows], [2, 3, 1])  # null, then desc

    def test_stable_sort_preserves_input_order(self):
        keys = [SortKey("score", ascending=True)]
        # Small budget forces several runs + a real k-way merge.
        sorter = make_sorter(self.tmp, keys, budget=200)
        rows_in = [(i, float(i % 3), f"r{i}") for i in range(30)]
        for chunk in (rows_in[:10], rows_in[10:20], rows_in[20:]):
            sorter.add_batch(Batch(SCHEMA, chunk))
        rows = sorter.sort_to_list()
        self.assertEqual([r[2] for r in rows], [f"r{i}" for i in
                         sorted(range(30), key=lambda i: i % 3)])
        # Python's sorted is stable; our output must match it exactly.
        self.assertEqual(rows, sorted(rows_in, key=lambda r: r[1]))

    def test_budget_smaller_than_one_batch(self):
        keys = [SortKey("id", ascending=False)]
        # Budget of 1 byte: every single row spills into its own run.
        sorter = make_sorter(self.tmp, keys, budget=1)
        rows_in = [(i, 1.0, f"n{i}") for i in range(25)]
        sorter.add_batch(Batch(SCHEMA, rows_in))
        rows = sorter.sort_to_list()
        self.assertEqual([r[0] for r in rows], list(range(24, -1, -1)))

    def test_null_and_nan_are_distinct(self):
        keys = [SortKey("score", ascending=True, nulls_first=True)]
        sorter = make_sorter(self.tmp, keys)
        sorter.add_batch(Batch(SCHEMA, [
            (1, NAN, "nan"),
            (2, None, "null"),
            (3, 1.5, "v"),
        ]))
        rows = sorter.sort_to_list()
        self.assertEqual([r[2] for r in rows], ["null", "v", "nan"])
        self.assertIsNone(rows[0][1])
        self.assertTrue(math.isnan(rows[2][1]))
        self.assertIsNotNone(rows[2][1])  # NaN is a value, not a null

    def test_integer_exactness(self):
        big = 2**80
        keys = [SortKey("id", ascending=True)]
        sorter = make_sorter(self.tmp, keys, budget=150)
        sorter.add_batch(Batch(SCHEMA, [
            (big + 2, 0.0, "c"),
            (big, 0.0, "a"),
            (big + 1, 0.0, "b"),
        ]))
        rows = sorter.sort_to_list()
        self.assertEqual([r[0] for r in rows], [big, big + 1, big + 2])
        self.assertEqual(rows[0][0], big)  # exact, no float rounding

    def test_empty_input(self):
        sorter = make_sorter(self.tmp, [SortKey("id")])
        sorter.add_batch(Batch(SCHEMA, []))
        self.assertEqual(sorter.sort_to_list(), [])

    def test_in_memory_sort_creates_no_temp_files(self):
        sorter = make_sorter(self.tmp, [SortKey("id")], budget=1 << 20)
        sorter.add_batch(Batch(SCHEMA, [(2, 0.0, "b"), (1, 0.0, "a")]))
        self.assertEqual([r[0] for r in sorter.sort_to_list()], [1, 2])
        self.assertEqual(os.listdir(self.tmp), [])

    def test_type_validation(self):
        with self.assertRaises(TypeError):
            Batch(SCHEMA, [(1, 0.0, 123)])            # str column gets int
        with self.assertRaises(TypeError):
            Batch(SCHEMA, [(True, 0.0, "x")])         # bool is not an int
        with self.assertRaises(ValueError):
            Batch(SCHEMA, [(1, 0.0)])                 # wrong arity


class TempFileAndFailureTest(unittest.TestCase):
    def setUp(self):
        self._tmp_ctx = tempfile.TemporaryDirectory()
        self.tmp = self._tmp_ctx.name

    def tearDown(self):
        self._tmp_ctx.cleanup()

    def _feed(self, sorter, n=40):
        rows = [(i, float(i % 5), f"r{i}") for i in range(n)]
        for start in range(0, n, 8):
            sorter.add_batch(Batch(SCHEMA, rows[start:start + 8]))
        return rows

    def test_temp_files_removed_after_success(self):
        sorter = make_sorter(self.tmp, [SortKey("id")], budget=200)
        self._feed(sorter)
        sorter.sort_to_list()
        self.assertEqual(os.listdir(self.tmp), [])

    def test_injected_merge_read_failure(self):
        """A failing run read must raise, clean up, and never fake success."""
        sorter = make_sorter(self.tmp, [SortKey("id")], budget=200)
        self._feed(sorter)

        real_iter = ExternalSorter._iter_run

        def flaky_iter(self, path):
            it = real_iter(self, path)
            yield next(it)                      # first record reads fine
            raise OSError("injected read failure")  # then the disk "fails"

        with mock.patch.object(ExternalSorter, "_iter_run", flaky_iter):
            with self.assertRaises(OSError):
                sorter.sort_to_list()
        # No leftover temp files pretending nothing happened...
        self.assertEqual(os.listdir(self.tmp), [])
        # ...and the sorter is immediately reusable.
        self._feed(sorter)
        rows = sorter.sort_to_list()
        self.assertEqual([r[0] for r in rows], list(range(40)))

    def test_truncated_run_file_is_detected(self):
        sorter = make_sorter(self.tmp, [SortKey("id")], budget=200)
        self._feed(sorter)
        # Corrupt one run on disk before the merge starts.
        victim = sorter._run_paths[0]
        with open(victim, "r+b") as fh:
            fh.truncate(os.path.getsize(victim) // 2)
        with self.assertRaises(SortError):
            sorter.sort_to_list()
        self.assertEqual(os.listdir(self.tmp), [])

    def test_cancel_then_reuse(self):
        sorter = make_sorter(self.tmp, [SortKey("id")], budget=200)
        self._feed(sorter)
        sorter.cancel()
        with self.assertRaises(SortCancelled):
            sorter.sort_to_list()
        self.assertEqual(os.listdir(self.tmp), [])
        # Reusable after cancellation.
        self._feed(sorter, n=10)
        rows = sorter.sort_to_list()
        self.assertEqual([r[0] for r in rows], list(range(10)))

    def test_cancel_during_merge(self):
        sorter = make_sorter(self.tmp, [SortKey("id")], budget=200)
        self._feed(sorter)

        real_iter = ExternalSorter._iter_run
        seen = {"n": 0}

        def cancelling_iter(self, path):
            for record in real_iter(self, path):
                seen["n"] += 1
                if seen["n"] == 3:
                    self.cancel()
                yield record

        with mock.patch.object(ExternalSorter, "_iter_run", cancelling_iter):
            with self.assertRaises(SortCancelled):
                sorter.sort_to_list()
        self.assertEqual(os.listdir(self.tmp), [])
        self._feed(sorter, n=5)
        self.assertEqual(len(sorter.sort_to_list()), 5)

    def test_cancel_during_add_batch(self):
        sorter = make_sorter(self.tmp, [SortKey("id")])
        sorter.cancel()
        with self.assertRaises(SortCancelled):
            sorter.add_batch(Batch(SCHEMA, [(1, 0.0, "a")]))
        sorter.add_batch(Batch(SCHEMA, [(2, 0.0, "b"), (1, 0.0, "a")]))
        self.assertEqual([r[0] for r in sorter.sort_to_list()], [1, 2])

    def test_failed_add_batch_resets_state(self):
        sorter = make_sorter(self.tmp, [SortKey("id")], budget=200)
        self._feed(sorter)
        bad_schema = (Column("x", ColumnType.INT),)
        with self.assertRaises(ValueError):
            sorter.add_batch(Batch(bad_schema, [(1,)]))
        self.assertEqual(os.listdir(self.tmp), [])  # spilled runs cleaned
        self._feed(sorter, n=6)
        self.assertEqual(len(sorter.sort_to_list()), 6)


if __name__ == "__main__":
    unittest.main(verbosity=2)
