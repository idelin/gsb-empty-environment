"""Unittest suite for the pull-based operator pipeline (stdlib only)."""

from __future__ import annotations

import csv
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from pipeline import (
    CancelToken, CancelledError, CsvScan, Filter, HashJoin, Limit,
    Operator, Project, run, sql_eq, sql_gt,
)


def write_csv(path, header, rows):
    with open(path, "w", newline="", encoding="utf-8") as fh:
        w = csv.writer(fh)
        if header:
            w.writerow(header)
        w.writerows(rows)


class ListSource(Operator):
    """Scripted source: yields the given batches, then None (EOF)."""

    def __init__(self, batches, token=None):
        super().__init__(token)
        self._batches = list(batches)
        self.released = False

    def _on_close(self):
        self.released = True

    def next_batch(self, max_rows):
        self._check_pullable()
        if not self._batches:
            return None
        return self._batches.pop(0)


class LimitEarlyStopTest(unittest.TestCase):
    def test_limit_1_does_not_scan_million_rows(self):
        path = os.path.join(self.tmp.name, "big.csv")
        n = 1_000_000
        with open(path, "w", encoding="utf-8") as fh:
            fh.write("id\n")
            fh.writelines(f"{i}\n" for i in range(n))

        scan = CsvScan(path, types=[int])
        limited = Limit(scan, 1)
        delivered = run(limited, batch_size=64)

        self.assertEqual(delivered, 1)
        self.assertLessEqual(scan.rows_read, 64)          # bounded, not 1M
        self.assertGreater(n - scan.rows_read, 999_000)   # almost nothing read
        self.assertTrue(scan.file_closed)                 # resource released
        self.assertTrue(scan.closed and limited.closed)

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)


class DownstreamErrorClosesUpstreamTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = os.path.join(self.tmp.name, "t.csv")
        write_csv(self.path, ["id", "v"], [[i, i * 10] for i in range(100)])

    def test_sink_error_closes_whole_pipeline(self):
        scan = CsvScan(self.path, types=[int, int])
        filt = Filter(scan, lambda r: True)
        limited = Limit(filt, 50)

        def boom(batch):
            raise RuntimeError("downstream failure")

        with self.assertRaises(RuntimeError):
            run(limited, sink=boom, batch_size=8)

        self.assertTrue(limited.closed)
        self.assertTrue(filt.closed)
        self.assertTrue(scan.closed)
        self.assertTrue(scan.file_closed)

    def test_operator_error_closes_its_children(self):
        scan = CsvScan(self.path, types=[int, int])

        class Exploding(Filter):
            def next_batch(self, max_rows):
                self._check_pullable()
                self._pull_child(max_rows)
                raise ValueError("operator blew up")

        expl = Exploding(scan, lambda r: True)
        with self.assertRaises(ValueError):
            run(expl, batch_size=4)
        self.assertTrue(expl.closed)
        self.assertTrue(scan.closed)
        self.assertTrue(scan.file_closed)


class CancellationTest(unittest.TestCase):
    def test_cancel_stops_pipeline_and_releases_resources(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        path = os.path.join(tmp.name, "t.csv")
        write_csv(path, ["id"], [[i] for i in range(10_000)])

        token = CancelToken()
        scan = CsvScan(path, types=[int], token=token)
        limited = Limit(scan, 10_000)
        seen = []

        def sink(batch):
            seen.extend(batch)
            token.cancel()  # cancel after the first delivered batch

        with self.assertRaises(CancelledError):
            run(limited, sink=sink, batch_size=100)

        self.assertEqual(len(seen), 100)      # stopped right after cancel
        self.assertLess(scan.rows_read, 1000) # bounded read
        self.assertTrue(scan.file_closed)
        self.assertTrue(scan.closed and limited.closed)


class CloseSemanticsTest(unittest.TestCase):
    def test_repeated_close_is_safe(self):
        src = ListSource([[(1,)], [(2,)]])
        limited = Limit(src, 10)
        limited.open()
        limited.close()
        limited.close()
        limited.close()  # no exception
        self.assertTrue(limited.closed and src.closed and src.released)

    def test_close_propagates_along_pipeline(self):
        src = ListSource([[(1,)]])
        filt = Filter(src, lambda r: True)
        proj = Project(filt, lambda r: r)
        proj.open()
        proj.close()
        self.assertTrue(src.closed and filt.closed and proj.closed)

    def test_empty_batch_is_not_eof(self):
        # Empty batches must not terminate the pull loop.
        src = ListSource([[], [], [(1,)], [], [(2,), (3,)], []])
        got = []
        delivered = run(src, sink=got.extend, batch_size=4)
        self.assertEqual(delivered, 3)
        self.assertEqual(got, [(1,), (2,), (3,)])

    def test_empty_batch_passes_through_filter(self):
        src = ListSource([[], [(5,)], []])
        filt = Filter(src, lambda r: True)
        got = []
        run(filt, sink=got.extend, batch_size=4)
        self.assertEqual(got, [(5,)])


class NullSemanticsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def test_null_comparison_is_unknown_and_filtered_out(self):
        path = os.path.join(self.tmp.name, "u.csv")
        # ages: 30, NULL, 30, 40, NULL
        write_csv(path, ["id", "age"], [[1, 30], [2, ""], [3, 30], [4, 40], [5, ""]])
        scan = CsvScan(path, types=[int, int])
        filt = Filter(scan, lambda r: sql_eq(r[1], 30))
        got = []
        run(filt, sink=got.extend, batch_size=2)
        self.assertEqual(got, [(1, 30), (3, 30)])  # NULLs rejected, dups kept

    def test_null_never_joins(self):
        users = os.path.join(self.tmp.name, "users.csv")
        orders = os.path.join(self.tmp.name, "orders.csv")
        write_csv(users, ["id", "name"], [[1, "a"], ["", "nulluser"], [2, "b"]])
        write_csv(orders, ["oid", "uid"], [[10, 1], [11, ""], [12, 2], [13, 1]])
        join = HashJoin(
            CsvScan(users, types=[int, str]),
            CsvScan(orders, types=[int, int]),
            left_key=lambda u: (u[0],),
            right_key=lambda o: (o[1],),
        )
        got = []
        run(join, sink=got.extend, batch_size=1)
        uids = sorted(r[0] for r in got)
        self.assertEqual(uids, [1, 1, 2])  # NULL ids on either side never match


class MultisetSemanticsTest(unittest.TestCase):
    def test_duplicates_preserved_through_filter_and_project(self):
        src = ListSource([[(1, "x"), (1, "x"), (2, "y"), (1, "x")]])
        filt = Filter(src, lambda r: sql_eq(r[0], 1))
        proj = Project(filt, lambda r: (r[1],))
        got = []
        run(proj, sink=got.extend, batch_size=2)
        self.assertEqual(got, [("x",), ("x",), ("x",)])  # 3 identical rows kept

    def test_join_is_bag_product(self):
        left = ListSource([[(1, "L1"), (1, "L2"), (2, "L3")]])       # key 1 x2
        right = ListSource([[(1, "R1"), (1, "R2"), (1, "R3"), (3, "R4")]])  # key 1 x3
        join = HashJoin(left, right,
                        left_key=lambda r: (r[0],), right_key=lambda r: (r[0],))
        got = []
        run(join, sink=got.extend, batch_size=2)
        self.assertEqual(len(got), 2 * 3)  # m x n for the matching key
        self.assertIn((1, "L1", 1, "R3"), got)
        self.assertIn((1, "L2", 1, "R1"), got)


class BoundedBatchTest(unittest.TestCase):
    def test_batches_never_exceed_max_rows(self):
        src = ListSource([[(i,) for i in range(10)]])  # source ignores hint
        limited = Limit(src, 7)
        sizes = []
        run(limited, sink=lambda b: sizes.append(len(b)), batch_size=3)
        self.assertTrue(all(s <= 3 for s in sizes))
        self.assertEqual(sum(sizes), 7)

    def test_scan_reads_in_bounded_batches(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        path = os.path.join(tmp.name, "t.csv")
        write_csv(path, ["id"], [[i] for i in range(1000)])
        scan = CsvScan(path, types=[int])
        scan.open()
        batch = scan.next_batch(16)
        self.assertEqual(len(batch), 16)
        self.assertEqual(scan.rows_read, 16)
        scan.close()
        self.assertTrue(scan.file_closed)


if __name__ == "__main__":
    unittest.main(verbosity=2)
