import csv
import gc
import os
import tempfile
import threading
import time
import unittest

from pullpipe import (
    AggDef,
    CancellationToken,
    CancelledError,
    CsvTable,
    Exploder,
    Filter,
    GeneratedTable,
    HashAggregate,
    HashJoin,
    Limit,
    ListTable,
    OperatorError,
    Pipeline,
    Project,
    Sort,
    TableScan,
    col,
    lit,
)


def million_scan(batch_size=256):
    table = GeneratedTable(("id", "cust_id", "amount"), 1_000_000,
                           lambda n: (n, n % 100, n % 7))
    return TableScan(table, batch_size=batch_size), table


class TestCancellation(unittest.TestCase):
    def test_cancel_from_consumer_thread(self):
        scan, table = million_scan(128)
        token = CancellationToken()
        pipe = Pipeline(scan, token)
        consumed = 0
        with self.assertRaises(CancelledError):
            for batch in pipe.batches():
                consumed += len(batch)
                if consumed >= 500:
                    token.cancel("stop")
        self.assertTrue(scan.closed)
        self.assertTrue(table.last_cursor.closed)
        self.assertLess(table.last_cursor.rows_generated, 5000)

    def test_cancel_from_another_thread(self):
        scan, table = million_scan(64)
        token = CancellationToken()
        pipe = Pipeline(scan, token)

        def stopper():
            time.sleep(0.05)
            token.cancel("external signal")

        t = threading.Thread(target=stopper)
        t.start()
        with self.assertRaises(CancelledError):
            list(pipe.iter_rows())
        t.join()
        self.assertTrue(scan.closed)
        self.assertLess(table.last_cursor.rows_generated, 1_000_000)

    def test_cancel_during_aggregate_build(self):
        scan, table = million_scan(128)
        agg = HashAggregate(
            scan,
            [AggDef("COUNT_ALL", name="n")],
            group_keys=[col("cust_id")],
            group_names=["g"],
        )
        token = CancellationToken()
        pipe = Pipeline(agg, token)
        token.cancel()  # 拉取前取消，首批边界即抛出
        with self.assertRaises(CancelledError):
            pipe.drain()
        self.assertTrue(scan.closed)

    def test_checkpoint_idempotent_message(self):
        token = CancellationToken()
        token.cancel("once")
        with self.assertRaises(CancelledError):
            token.raise_if_cancelled()
        token.cancel("twice")  # 不覆盖首次原因
        try:
            token.raise_if_cancelled()
        except CancelledError as exc:
            self.assertEqual(str(exc), "once")
        else:
            self.fail("expected CancelledError")


class TestErrorAndClose(unittest.TestCase):
    def test_downstream_error_closes_upstream(self):
        scan, table = million_scan(128)
        root = Exploder(scan)
        pipe = Pipeline(root)
        with self.assertRaises(OperatorError) as cm:
            pipe.drain()
        self.assertIsInstance(cm.exception.cause, RuntimeError)
        self.assertIn("boom", str(cm.exception.cause))
        self.assertTrue(scan.closed)
        self.assertTrue(table.last_cursor.closed)
        self.assertEqual(table.last_cursor.rows_generated, 128)

    def test_error_closes_all_branches(self):
        # join 的左右两侧以及更上游都必须被关闭
        left, lt = million_scan(64)
        right_scan = TableScan(
            ListTable(("cust_id", "name"), [(i, f"c{i}") for i in range(100)]), 64
        )
        join = HashJoin(left, right_scan, col("cust_id"), col("cust_id"), batch_size=32)
        root = Exploder(join)
        with self.assertRaises(OperatorError):
            Pipeline(root).drain()
        self.assertTrue(left.closed and right_scan.closed)
        self.assertTrue(lt.last_cursor.closed)

    def test_double_close_safe(self):
        scan, _ = million_scan(128)
        pipe = Pipeline(Limit(scan, 3))
        pipe.drain()
        pipe.close()
        pipe.close()
        scan.close()
        self.assertEqual(scan.release_count, 1)
        self.assertTrue(pipe.drain() == [])

    def test_close_without_pull(self):
        scan, table = million_scan(128)
        pipe = Pipeline(scan)
        pipe.close()
        self.assertTrue(scan.closed)
        self.assertIsNone(table.last_cursor)  # 没拉过就没打开过

    def test_context_manager_early_break(self):
        scan, table = million_scan(10)
        with Pipeline(Limit(scan, 10)) as pipe:
            rows = pipe.drain()
        self.assertEqual(len(rows), 10)
        self.assertTrue(scan.closed)
        self.assertEqual(table.last_cursor.rows_generated, 10)

    def test_generator_gc_releases_resources(self):
        scan, table = million_scan(128)
        pipe = Pipeline(scan)
        gen = pipe.iter_rows()
        self.assertEqual(next(gen), (0, 0, 0))
        del gen
        gc.collect()
        self.assertTrue(scan.closed)
        self.assertTrue(table.last_cursor.closed)

    def test_run_callback_exception_releases(self):
        scan, table = million_scan(128)

        def bad(batch):
            raise ValueError("consumer blew up")

        pipe = Pipeline(scan)
        with self.assertRaises(ValueError):
            pipe.run(bad)
        self.assertTrue(scan.closed)
        self.assertEqual(table.last_cursor.rows_generated, 128)


class TestCsvResource(unittest.TestCase):
    def test_csv_limit_closes_handle(self):
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "t.csv")
            with open(path, "w", newline="", encoding="utf-8") as f:
                w = csv.writer(f)
                w.writerow(["k", "v"])
                w.writerows(([i % 4, i] for i in range(50_000)))
            table = CsvTable(path, ("k", "v"))
            scan = TableScan(table, batch_size=8)
            pipe = Pipeline(Limit(scan, 5))
            rows = pipe.drain()
            self.assertEqual(len(rows), 5)
            self.assertTrue(table.last_cursor.closed)
            self.assertTrue(table.last_cursor._file.closed)
            pipe.close()  # 重复关闭安全
            self.assertTrue(table.last_cursor._file.closed)


class TestEndToEnd(unittest.TestCase):
    def test_full_pipeline_shape(self):
        orders = TableScan(
            GeneratedTable(("id", "cust_id", "amount"), 5_000,
                           lambda n: (n, n % 50, n % 13)),
            batch_size=512,
        )
        customers = TableScan(
            ListTable(("cid", "name"), [(i, f"c{i}") for i in range(50)]), 512
        )
        joined = HashJoin(orders, customers, col("cust_id"), col("cid"))
        agg = HashAggregate(
            joined,
            [AggDef("COUNT_ALL", name="n"), AggDef("SUM", col("amount"), name="total")],
            group_keys=[col("cust_id")],
            group_names=["cust_id"],
        )
        top = Limit(Sort(agg, [col("total")], descs=[True]), 5)
        pipe = Pipeline(top)
        rows = pipe.drain()
        self.assertEqual(len(rows), 5)
        totals = [r[2] for r in rows]
        self.assertEqual(totals, sorted(totals, reverse=True))
        self.assertTrue(orders.closed and customers.closed)

    def test_batch_sizes_are_bounded(self):
        orders = TableScan(
            GeneratedTable(("x",), 10_000, lambda n: (n,)), batch_size=37
        )
        pipe = Pipeline(orders)
        sizes = []
        for batch in pipe.drain_batches():
            sizes.append(len(batch))
        self.assertTrue(all(s <= 37 for s in sizes))
        self.assertEqual(sum(sizes), 10_000)
        self.assertEqual(sizes[-1], 10_000 % 37)

    def test_filter_project_join_limit_columns(self):
        left = TableScan(ListTable(("k", "v"), [(1, 10), (2, 20), (3, 30)]), 2)
        right = TableScan(ListTable(("k", "w"), [(2, "x"), (3, "y")]), 2)
        plan = Limit(
            Project(
                Filter(
                    HashJoin(left, right, col("k"), col("k")),
                    col("v") > lit(15),
                ),
                [col("w"), col("v")],
                ["w", "v"],
            ),
            1,
        )
        rows = Pipeline(plan).drain()
        self.assertEqual(rows, [("x", 20)])
        self.assertTrue(left.closed and right.closed)


if __name__ == "__main__":
    unittest.main()
