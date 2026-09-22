import csv
import os
import tempfile
import unittest

from pullpipe import CsvTable, GeneratedTable, ListTable, Schema, TableScan, Limit, Pipeline


class TestCursors(unittest.TestCase):
    def test_list_cursor_bounded_reads(self):
        t = ListTable(("a", "b"), [(i, i * 2) for i in range(10)])
        cur = t.open()
        self.assertEqual(len(cur.read_rows(3)), 3)
        self.assertEqual(len(cur.read_rows(4)), 4)
        self.assertEqual(len(cur.read_rows(100)), 3)
        self.assertEqual(cur.read_rows(100), [])  # EOF 之后稳定空列表
        cur.close()
        self.assertTrue(cur.closed)
        self.assertEqual(cur.read_rows(5), [])    # close 后读取安全

    def test_generated_cursor_lazy_and_close_releases(self):
        produced = []

        def maker(n):
            produced.append(n)
            return (n,)

        t = GeneratedTable(("a",), 1_000_000, maker)
        cur = t.open()
        self.assertEqual(produced, [])  # 构造/open 都不产行
        first = cur.read_rows(3)
        self.assertEqual(len(first), 3)
        self.assertEqual(produced, [0, 1, 2])
        cur.close()
        self.assertTrue(cur.closed)
        cur.close()  # 幂等
        self.assertEqual(cur.read_rows(10), [])
        self.assertEqual(produced, [0, 1, 2], "close 后生成器停止，未继续扫描")

    def test_close_before_any_read(self):
        calls = []
        t = GeneratedTable(("a",), 1_000_000, lambda n: (calls.append(n) or n,))
        cur = t.open()
        cur.close()
        self.assertEqual(calls, [])
        cur.close()  # 重复 close 安全
        self.assertEqual(cur.read_rows(10), [])

    def test_csv_cursor_closes_file(self):
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "t.csv")
            with open(path, "w", newline="", encoding="utf-8") as f:
                w = csv.writer(f)
                w.writerow(["k", "v"])
                w.writerow([1, "x"])
                w.writerow([2, "y"])
            table = CsvTable(path, ("k", "v"))
            cur = table.open()
            rows = cur.read_rows(10)
            self.assertEqual(rows, [("1", "x"), ("2", "y")])
            self.assertTrue(cur.closed)  # 读到 EOF 自动关闭
            self.assertTrue(cur._file.closed)
            cur.close()  # 幂等

    def test_csv_header_mismatch(self):
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "t.csv")
            with open(path, "w", newline="", encoding="utf-8") as f:
                f.write("a,b\n1,2\n")
            with self.assertRaises(ValueError):
                CsvTable(path, ("x", "b")).open()

    def test_width_validation(self):
        with self.assertRaises(ValueError):
            ListTable(("a", "b"), [(1, 2, 3)])

    def test_schema_lookup(self):
        s = Schema(("a", "b", "c"))
        self.assertEqual(s.index("c"), 2)
        with self.assertRaises(KeyError):
            s.index("zzz")


class TestLimitStopsScan(unittest.TestCase):
    def test_limit1_on_million_rows_generates_one(self):
        t = GeneratedTable(("a",), 1_000_000, lambda n: (n,))
        scan = TableScan(t, batch_size=1)
        pipe = Pipeline(Limit(scan, 1))
        rows = pipe.drain()
        self.assertEqual(rows, [(0,)])
        self.assertEqual(t.last_cursor.rows_generated, 1)
        self.assertTrue(scan.closed)
        self.assertTrue(t.last_cursor.closed)
        self.assertEqual(pipe.drain(), [])  # 关闭后再驱动安全

    def test_limit1_never_exceeds_one_batch(self):
        # 通常 batch_size > 1 时最多多读一个批次，绝不会全量扫描
        t = GeneratedTable(("a",), 1_000_000, lambda n: (n,))
        scan = TableScan(t, batch_size=64)
        self.assertEqual(Pipeline(Limit(scan, 1)).drain(), [(0,)])
        self.assertLessEqual(t.last_cursor.rows_generated, 64)
        self.assertTrue(scan.closed)

    def test_limit0_never_pulls(self):
        t = GeneratedTable(("a",), 1_000_000, lambda n: (n,))
        scan = TableScan(t, batch_size=64)
        pipe = Pipeline(Limit(scan, 0))
        self.assertEqual(pipe.drain(), [])
        self.assertTrue(scan.closed)
        self.assertIsNone(t.last_cursor)  # 扫描从未打开

    def test_limit_smaller_than_first_batch(self):
        t = ListTable(("a",), [(i,) for i in range(100)])
        scan = TableScan(t, batch_size=50)
        rows = Pipeline(Limit(scan, 7)).drain()
        self.assertEqual(rows, [(i,) for i in range(7)])
        self.assertTrue(scan.closed)


if __name__ == "__main__":
    unittest.main()
