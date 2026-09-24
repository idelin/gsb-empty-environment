import math
import unittest

from mvcc_store import (
    Batch,
    ColumnSpec,
    ColumnType,
    MVCCStore,
    values_equal,
)


def make_store():
    return MVCCStore([
        ColumnSpec("v", ColumnType.INT),
        ColumnSpec("x", ColumnType.FLOAT),
        ColumnSpec("s", ColumnType.STRING),
    ])


def batch(rows):
    return Batch(
        [ColumnSpec("v", ColumnType.INT), ColumnSpec("x", ColumnType.FLOAT),
         ColumnSpec("s", ColumnType.STRING)],
        rows,
    )


class TestThreeVersionScenario(unittest.TestCase):
    """题目场景：v1 值 5，v2 改 7，v3 删行；三个版本各自查询正确。"""

    def setUp(self):
        self.store = make_store()
        self.v1 = self.store.apply_batch(batch([{"_row_id": 1, "v": 5}]))
        self.v2 = self.store.apply_batch(batch([{"_row_id": 1, "v": 7}]))
        self.v3 = self.store.delete_rows([1])

    def test_versions_are_sequential(self):
        self.assertEqual((self.v1, self.v2, self.v3), (1, 2, 3))

    def test_query_at_v1_sees_5(self):
        self.assertEqual(self.store.get_at(1, self.v1), {"v": 5})

    def test_query_at_v2_sees_7(self):
        self.assertEqual(self.store.get_at(1, self.v2), {"v": 7})

    def test_query_at_v3_row_deleted(self):
        self.assertIsNone(self.store.get_at(1, self.v3))
        self.assertEqual(self.store.scan_at(self.v3), {})

    def test_snapshot_isolation(self):
        s1 = self.store.open_snapshot()  # 版本 3 的当前快照
        self.assertIsNone(s1.get(1))
        s1.close()

    def test_compact_with_active_snapshot_at_v2_keeps_7(self):
        """活跃快照 2 存在时压缩不能丢 7，也不能让快照 2 看到删除。"""
        snap2 = self.store.open_snapshot()
        # 把快照"放"在版本 2：直接构造一个版本 2 的活跃快照
        snap2.close()
        from mvcc_store import Snapshot
        snap2 = Snapshot(self.store, self.v2)
        self.store._snapshots.append(snap2)

        self.assertEqual(self.store.gc_watermark(), self.v2)
        self.store.compact()

        # 快照 2 仍读到 7，且看不到 v3 的删除
        self.assertEqual(snap2.get(1), {"v": 7})
        # 最新版本仍能看到删除生效
        self.assertIsNone(self.store.get_at(1, self.v3))
        # v1 的旧值 5 已被折叠（水位以下无读者），但 v2 读到的仍是 7
        self.assertEqual(self.store.get_at(1, self.v2), {"v": 7})
        snap2.close()

    def test_compact_without_snapshots_reclaims_tombstone(self):
        self.store.compact()
        self.assertEqual(self.store.layers, [])  # 行已删除，全部回收
        self.assertEqual(self.store.scan_at(self.v3), {})


class TestWatermark(unittest.TestCase):
    def test_watermark_tracks_oldest_snapshot(self):
        store = make_store()
        store.apply_batch(batch([{"_row_id": 1, "v": 1}]))
        store.apply_batch(batch([{"_row_id": 1, "v": 2}]))
        store.apply_batch(batch([{"_row_id": 1, "v": 3}]))
        self.assertEqual(store.gc_watermark(), 3)  # 无快照 -> 当前版本

        from mvcc_store import Snapshot
        s2 = Snapshot(store, 2)
        store._snapshots.append(s2)
        s3 = store.open_snapshot()
        self.assertEqual(store.gc_watermark(), 2)  # 最老活跃快照
        s2.close()
        self.assertEqual(store.gc_watermark(), 3)
        s3.close()
        self.assertEqual(store.gc_watermark(), 3)

    def test_compact_merges_only_below_watermark(self):
        store = make_store()
        for i in range(1, 6):
            store.apply_batch(batch([{"_row_id": 1, "v": i * 10}]))
        from mvcc_store import Snapshot
        s3 = Snapshot(store, 3)
        store._snapshots.append(s3)
        store.compact()
        versions = [l.version for l in store.layers]
        self.assertEqual(versions, [3, 4, 5])  # 1..3 折叠为基线层@3
        self.assertEqual(s3.get(1), {"v": 30})
        self.assertEqual(store.get_at(1, 5), {"v": 50})
        s3.close()

    def test_compaction_never_exposes_newer_values(self):
        """压缩后，旧快照可见值逐版本保持不变。"""
        store = make_store()
        store.apply_batch(batch([{"_row_id": 1, "v": 5, "s": "a"}]))
        store.apply_batch(batch([{"_row_id": 1, "v": 7}]))
        store.apply_batch(batch([{"_row_id": 2, "v": 9}]))
        store.delete_rows([1])
        before = {v: store.scan_at(v) for v in range(0, 5)}

        from mvcc_store import Snapshot
        s2 = Snapshot(store, 2)
        store._snapshots.append(s2)
        store.compact()
        for v in (2, 3, 4):  # 水位及以上版本视图必须逐字节一致
            self.assertEqual(store.scan_at(v), before[v])
        s2.close()


class TestNullVsNaN(unittest.TestCase):
    def test_null_and_nan_are_distinct(self):
        store = make_store()
        store.apply_batch(batch([
            {"_row_id": 1, "x": None},                # null
            {"_row_id": 2, "x": float("nan")},        # NaN
        ]))
        null_val = store.get_at(1, 1)["x"]
        nan_val = store.get_at(2, 1)["x"]
        self.assertIsNone(null_val)
        self.assertTrue(math.isnan(nan_val))
        self.assertFalse(values_equal(null_val, nan_val))
        self.assertFalse(values_equal(nan_val, null_val))
        self.assertTrue(values_equal(nan_val, float("nan")))
        self.assertTrue(values_equal(None, None))

    def test_update_null_to_nan_and_back(self):
        store = make_store()
        store.apply_batch(batch([{"_row_id": 1, "x": None}]))
        store.apply_batch(batch([{"_row_id": 1, "x": float("nan")}]))
        store.apply_batch(batch([{"_row_id": 1, "x": 1.5}]))
        self.assertIsNone(store.get_at(1, 1)["x"])
        self.assertTrue(math.isnan(store.get_at(1, 2)["x"]))
        self.assertEqual(store.get_at(1, 3)["x"], 1.5)


class TestExactIntegers(unittest.TestCase):
    def test_big_int_exact(self):
        store = make_store()
        big = 2**63 + 12345  # 超出 int64，float 会丢精度
        store.apply_batch(batch([{"_row_id": 1, "v": big}]))
        got = store.get_at(1, 1)["v"]
        self.assertEqual(got, big)
        self.assertIs(type(got), int)

    def test_int_arithmetic_exact(self):
        a, b = 10**30, 7
        self.assertEqual(a + b, 1000000000000000000000000000007)

    def test_int_column_rejects_float_and_bool(self):
        store = make_store()
        with self.assertRaises(TypeError):
            store.apply_batch(batch([{"_row_id": 1, "v": 1.5}]))
        with self.assertRaises(TypeError):
            store.apply_batch(batch([{"_row_id": 1, "v": True}]))

    def test_type_validation(self):
        store = make_store()
        with self.assertRaises(TypeError):
            store.apply_batch(batch([{"_row_id": 1, "s": 123}]))
        with self.assertRaises(ValueError):
            store.apply_batch(batch([{"v": 1}]))  # 缺 _row_id
        with self.assertRaises(ValueError):
            store.apply_batch(batch([{"_row_id": 1, "nope": 1}]))  # 未知列


class TestTombstoneSemantics(unittest.TestCase):
    def test_delete_then_reinsert(self):
        store = make_store()
        store.apply_batch(batch([{"_row_id": 1, "v": 5}]))
        store.delete_rows([1])
        store.apply_batch(batch([{"_row_id": 1, "v": 42}]))
        self.assertIsNone(store.get_at(1, 2))
        self.assertEqual(store.get_at(1, 3), {"v": 42})

    def test_partial_column_update(self):
        store = make_store()
        store.apply_batch(batch([{"_row_id": 1, "v": 5, "s": "hello"}]))
        store.apply_batch(batch([{"_row_id": 1, "v": 7}]))  # 只改 v
        row = store.get_at(1, 2)
        self.assertEqual(row, {"v": 7, "s": "hello"})


if __name__ == "__main__":
    unittest.main(verbosity=2)
