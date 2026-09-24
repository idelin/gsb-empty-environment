"""Unit tests for external_sort (Python 3.11, stdlib unittest)."""

import math
import os
import tempfile
import unittest

from external_sort import (
    Batch,
    ExternalSorter,
    Schema,
    SortCancelled,
    SortKey,
    _RunReader,
)

NAN = float("nan")


def flatten(batches):
    return [row for batch in batches for row in batch.rows]


class SchemaAndBatchTest(unittest.TestCase):
    def test_row_width_mismatch_rejected(self):
        schema = Schema([("a", "int")])
        with self.assertRaises(ValueError):
            Batch(schema, [(1, 2)])

    def test_type_validation(self):
        schema = Schema([("i", "int"), ("f", "float"), ("s", "str"), ("b", "bool")])
        # valid: null allowed everywhere, int allowed in float column
        Batch(schema, [(1, 2, "x", True), (None, None, None, None)])
        bad_rows = [
            (1.5, 1.0, "x", True),   # float in int column
            (True, 1.0, "x", True),  # bool is not accepted as int
            (1, "y", "x", True),     # str in float column
            (1, 1.0, 3, True),       # int in str column
            (1, 1.0, "x", 1),        # int in bool column
        ]
        for row in bad_rows:
            with self.subTest(row=row):
                with self.assertRaises(TypeError):
                    Batch(schema, [row])

    def test_unknown_column_type_rejected(self):
        with self.assertRaises(ValueError):
            Schema([("a", "date")])


class SortingTest(unittest.TestCase):
    def setUp(self):
        self.schema = Schema([("k", "int"), ("v", "str")])

    def sort_rows(self, rows, keys, budget=3):
        sorter = ExternalSorter(self.schema, keys, max_rows_in_memory=budget)
        sorter.add_batch(Batch(self.schema, rows))
        return flatten(sorter.sort())

    def test_simple_ascending(self):
        rows = [(5, "e"), (1, "a"), (3, "c"), (2, "b"), (4, "d")]
        out = self.sort_rows(rows, [SortKey("k")])
        self.assertEqual([r[0] for r in out], [1, 2, 3, 4, 5])

    def test_mixed_ascending_descending(self):
        schema = Schema([("a", "int"), ("b", "int"), ("v", "str")])
        keys = [SortKey("a", ascending=True), SortKey("b", ascending=False)]
        sorter = ExternalSorter(schema, keys, max_rows_in_memory=2)
        sorter.add_batch(Batch(schema, [(1, 1, "x"), (2, 1, "y"), (1, 3, "z"),
                                        (2, 0, "w"), (1, 2, "q")]))
        out = flatten(sorter.sort())
        self.assertEqual([r[2] for r in out], ["z", "q", "x", "y", "w"])

    def test_stability_equal_keys_keep_input_order(self):
        keys = [SortKey("k")]
        expected = [(i % 4, f"row{i}") for i in range(50)]
        sorter = ExternalSorter(self.schema, keys, max_rows_in_memory=3)
        # feed in several batches so many runs are spilled
        for start in range(0, 50, 7):
            sorter.add_batch(Batch(self.schema, expected[start:start + 7]))
        out = flatten(sorter.sort())
        self.assertEqual(out, sorted(expected, key=lambda r: r[0]))

    def test_budget_smaller_than_batch(self):
        rows = [(i, str(i)) for i in (9, 4, 7, 1, 8, 2, 6)]
        out = self.sort_rows(rows, [SortKey("k")], budget=1)
        self.assertEqual([r[0] for r in out], [1, 2, 4, 6, 7, 8, 9])

    def test_empty_input(self):
        sorter = ExternalSorter(self.schema, [SortKey("k")], max_rows_in_memory=2)
        sorter.add_batch(Batch(self.schema, []))
        self.assertEqual(sorter.sort(), [])

    def test_output_batching(self):
        rows = [(i, str(i)) for i in range(10)]
        sorter = ExternalSorter(self.schema, [SortKey("k")], max_rows_in_memory=4)
        sorter.add_batch(Batch(self.schema, rows))
        batches = sorter.sort()
        self.assertEqual([len(b) for b in batches], [4, 4, 2])

    def test_exact_integer_sorting(self):
        big = 2**63
        rows = [(big + 5, "a"), (big + 1, "b"), (big + 3, "c"),
                (-big - 2, "d"), (0, "e")]
        out = self.sort_rows(rows, [SortKey("k")], budget=2)
        self.assertEqual([r[0] for r in out],
                         [-big - 2, 0, big + 1, big + 3, big + 5])


class NullAndNaNTest(unittest.TestCase):
    def setUp(self):
        self.schema = Schema([("x", "float"), ("tag", "str")])

    def sort_x(self, values, ascending=True, nulls_first=False, budget=2):
        keys = [SortKey("x", ascending=ascending, nulls_first=nulls_first)]
        sorter = ExternalSorter(self.schema, keys, max_rows_in_memory=budget)
        sorter.add_batch(Batch(self.schema, [(v, str(i)) for i, v in enumerate(values)]))
        return [r[0] for r in flatten(sorter.sort())]

    def test_nulls_first_ascending(self):
        out = self.sort_x([3.0, None, 1.0, None, 2.0], nulls_first=True)
        self.assertEqual(out, [None, None, 1.0, 2.0, 3.0])

    def test_nulls_last_descending(self):
        # null placement is explicit and not flipped by descending
        out = self.sort_x([3.0, None, 1.0, 2.0], ascending=False, nulls_first=False)
        self.assertEqual(out, [3.0, 2.0, 1.0, None])

    def test_nulls_first_kept_under_descending(self):
        out = self.sort_x([3.0, None, 1.0], ascending=False, nulls_first=True)
        self.assertEqual(out, [None, 3.0, 1.0])

    def test_nan_is_not_null_and_sorts_after_numbers(self):
        out = self.sort_x([NAN, None, 1.0, float("inf"), -1.0],
                          nulls_first=True, budget=3)
        self.assertIsNone(out[0])
        self.assertEqual(out[1:4], [-1.0, 1.0, float("inf")])
        self.assertTrue(math.isnan(out[4]))

    def test_nan_distinct_from_null_under_descending(self):
        out = self.sort_x([NAN, None, 1.0], ascending=False,
                          nulls_first=False, budget=1)
        self.assertTrue(math.isnan(out[0]))
        self.assertEqual(out[1], 1.0)
        self.assertIsNone(out[2])


class TempFileLifecycleTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.schema = Schema([("k", "int")])

    def make_sorter(self, **kw):
        return ExternalSorter(self.schema, [SortKey("k")],
                              max_rows_in_memory=2,
                              temp_dir=self.tmp.name, **kw)

    def test_temp_files_removed_on_success(self):
        sorter = self.make_sorter()
        sorter.add_batch(Batch(self.schema, [(i,) for i in range(9)]))
        self.assertTrue(sorter.run_file_paths)  # runs were spilled
        out = flatten(sorter.sort())
        self.assertEqual([r[0] for r in out], list(range(9)))
        self.assertEqual(os.listdir(self.tmp.name), [])

    def test_injected_merge_read_error_no_fake_success(self):
        sorter = self.make_sorter()
        sorter.add_batch(Batch(self.schema, [(i,) for i in range(9)]))
        # corrupt every spilled run file before the merge reads them
        for path in sorter.run_file_paths:
            with open(path, "r+b") as fh:
                fh.truncate(3)
        with self.assertRaises(Exception):
            sorter.sort()  # must raise, never return partial output
        self.assertEqual(os.listdir(self.tmp.name), [])  # temp files cleaned
        # sorter is reusable after the failure
        sorter.add_batch(Batch(self.schema, [(2,), (1,)]))
        self.assertEqual([r[0] for r in flatten(sorter.sort())], [1, 2])

    def test_failing_reader_injection(self):
        class ExplodingReader:
            def __init__(self, path):
                pass

            def next(self):
                raise IOError("injected read failure")

        sorter = self.make_sorter(reader_factory=ExplodingReader)
        sorter.add_batch(Batch(self.schema, [(i,) for i in range(5)]))
        with self.assertRaises(IOError):
            sorter.sort()
        self.assertEqual(os.listdir(self.tmp.name), [])


class CancelTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.schema = Schema([("k", "int")])

    def test_cancel_before_sort_and_reuse(self):
        sorter = ExternalSorter(self.schema, [SortKey("k")],
                                max_rows_in_memory=2, temp_dir=self.tmp.name)
        sorter.add_batch(Batch(self.schema, [(3,), (1,), (2,)]))
        sorter.cancel()
        with self.assertRaises(SortCancelled):
            sorter.sort()
        self.assertEqual(os.listdir(self.tmp.name), [])
        # usable again after cancellation
        sorter.add_batch(Batch(self.schema, [(5,), (4,)]))
        self.assertEqual([r[0] for r in flatten(sorter.sort())], [4, 5])

    def test_cancel_during_merge(self):
        sorter = ExternalSorter(self.schema, [SortKey("k")],
                                max_rows_in_memory=2, temp_dir=self.tmp.name)

        class CancellingReader:
            def __init__(self, path):
                self._inner = _RunReader(path)
                self._reads = 0

            def next(self):
                self._reads += 1
                if self._reads == 2:
                    sorter.cancel()
                return self._inner.next()

            def close(self):
                self._inner.close()

        sorter._reader_factory = CancellingReader
        sorter.add_batch(Batch(self.schema, [(i,) for i in range(8)]))
        with self.assertRaises(SortCancelled):
            sorter.sort()
        self.assertEqual(os.listdir(self.tmp.name), [])
        sorter.add_batch(Batch(self.schema, [(1,), (0,)]))
        sorter._reader_factory = _RunReader
        self.assertEqual([r[0] for r in flatten(sorter.sort())], [0, 1])


class ReferenceModelTest(unittest.TestCase):
    """Cross-check against an independent in-memory reference comparator."""

    def test_randomized_against_reference(self):
        import functools
        import random

        rng = random.Random(20260924)
        schema = Schema([("a", "int"), ("b", "float"), ("c", "str")])
        keys = [SortKey("a", ascending=False, nulls_first=True),
                SortKey("b", ascending=True, nulls_first=False),
                SortKey("c", ascending=False)]

        def ref_cmp(ra, rb):
            for idx, asc, nf in ((0, False, True), (1, True, False), (2, False, False)):
                a, b = ra[idx], rb[idx]
                if a is None or b is None:
                    if a is None and b is None:
                        continue
                    # null placement is explicit, never flipped by direction
                    return (-1 if a is None else 1) if nf else (1 if a is None else -1)
                else:
                    an = isinstance(a, float) and math.isnan(a)
                    bn = isinstance(b, float) and math.isnan(b)
                    if an or bn:
                        c = 0 if (an and bn) else (1 if an else -1)
                    else:
                        c = (a > b) - (a < b)
                if c:
                    return c if asc else -c
            return 0

        pool_a = [None, 1, 2, 3]
        pool_b = [None, 0.5, NAN, 2.5]
        pool_c = [None, "p", "q"]
        rows = [(rng.choice(pool_a), rng.choice(pool_b), rng.choice(pool_c))
                for _ in range(120)]
        # reference: stable sort with the independent comparator
        expected = sorted(rows, key=functools.cmp_to_key(
            lambda ra, rb: ref_cmp(ra, rb)))

        sorter = ExternalSorter(schema, keys, max_rows_in_memory=5)
        for start in range(0, len(rows), 13):
            sorter.add_batch(Batch(schema, rows[start:start + 13]))
        got = flatten(sorter.sort())

        def norm(row):
            return tuple("NAN" if isinstance(v, float) and math.isnan(v) else v
                         for v in row)

        self.assertEqual([norm(r) for r in got], [norm(r) for r in expected])


if __name__ == "__main__":
    unittest.main(verbosity=2)
