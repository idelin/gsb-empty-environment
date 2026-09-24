"""mvcc_store 的单元测试（Python 3.11，仅标准库）。"""

import math
import unittest

from mvcc_store import (
    Batch,
    ColumnType,
    MvccStore,
    TOMBSTONE,
    values_equal,
)

SCHEMA = {"balance": ColumnType.INT, "score": ColumnType.FLOAT, "name": ColumnType.TEXT}


def make_store():
    return MvccStore(SCHEMA)


def batch(rows):
    return Batch.of(SCHEMA, rows)


class ThreeVersionScenarioTest(unittest.TestCase):
    """版本1值5、版本2改7、版本3删行，三个版本查询各自正确。"""

    def setUp(self):
        self.store = make_store()
        self.v1 = self.store.apply_batch(batch([
            {"id": 1, "balance": 5, "score": 1.5, "name": "a"},
            {"id": 2, "balance": 100, "score": 2.5, "name": "b"},
        ]))
        self.v2 = self.store.apply_batch(batch([
            {"id": 1, "balance": 7, "score": 1.5, "name": "a"},
        ]))
        self.v3 = self.store.apply_batch(batch([]), deletes=[1])

    def test_version_numbers(self):
        self.assertEqual((self.v1, self.v2, self.v3), (1, 2, 3))

    def test_query_at_v1(self):
        self.assertEqual(self.store.read(1, "balance", 1), (True, 5))
        self.assertTrue(self.store.row_exists(1, 1))

    def test_query_at_v2(self):
        self.assertEqual(self.store.read(1, "balance", 2), (True, 7))

    def test_query_at_v3_row_deleted(self):
        self.assertEqual(self.store.read(1, "balance", 3), (False, None))
        self.assertFalse(self.store.row_exists(1, 3))
        ids = [r["id"] for r in self.store.scan(3)]
        self.assertEqual(ids, [2])  # 只剩行2

    def test_unaffected_row_stable_across_versions(self):
        for v in (1, 2, 3):
            self.assertEqual(self.store.read(2, "balance", v), (True, 100))


class CompactionTest(unittest.TestCase):
    """压缩与墓碑/水位行为。"""

    def setUp(self):
        self.store = make_store()
        self.store.apply_batch(batch([
            {"id": 1, "balance": 5, "score": 1.5, "name": "a"},
            {"id": 2, "balance": 100, "score": 2.5, "name": "b"},
        ]))  # v1
        self.store.apply_batch(batch([
            {"id": 1, "balance": 7, "score": 1.5, "name": "a"},
        ]))  # v2
        self.store.apply_batch(batch([]), deletes=[1])  # v3

    def test_compaction_with_active_snapshot_2_keeps_7(self):
        snap = self.store.snapshot(2)
        try:
            stats = self.store.compact()
            self.assertEqual(stats["watermark"], 2)
            # 活跃快照2仍读到7，不能丢
            self.assertEqual(snap.get(1, "balance"), 7)
            self.assertTrue(snap.row_exists(1))
            # 当前版本仍看到行1已删除
            self.assertFalse(self.store.row_exists(1))
            # 水位以上的删除层未被合并掉
            self.assertEqual(self.store.read(1, "balance", 3), (False, None))
        finally:
            snap.close()

    def test_merge_does_not_leak_new_values_to_old_snapshot(self):
        snap1 = self.store.snapshot(1)
        snap2 = self.store.snapshot(2)
        try:
            self.store.compact()  # 水位=1，只合并 v1
            self.assertEqual(snap1.get(1, "balance"), 5)  # 旧快照看不到新值7
            self.assertEqual(snap2.get(1, "balance"), 7)
        finally:
            snap1.close()
            snap2.close()

    def test_watermark_advances_after_snapshot_release(self):
        snap1 = self.store.snapshot(1)
        snap2 = self.store.snapshot(2)
        self.assertEqual(self.store.watermark, 1)
        snap1.close()
        self.assertEqual(self.store.watermark, 2)
        snap2.close()
        self.assertEqual(self.store.watermark, 3)  # 无快照时为当前版本

    def test_tombstone_reclaimed_when_no_snapshots(self):
        stats = self.store.compact()  # 无快照，水位=3，全部合并
        self.assertEqual(stats["merged_layers"], 3)
        self.assertEqual(stats["layers_after"], 1)
        # 行1的墓碑被回收（每列一个，共3列）
        self.assertEqual(stats["reclaimed_tombstones"], 3)
        # 基底层中不再有任何行1的痕迹
        self.assertNotIn(1, self.store.layers[0].updates["balance"])
        # 行2数据保留
        self.assertEqual(self.store.read(2, "balance"), (True, 100))

    def test_layer_count_reduced_and_reads_still_correct(self):
        self.assertEqual(len(self.store.layers), 3)
        with self.store.snapshot(2):
            self.store.compact()
            self.assertEqual(len(self.store.layers), 2)  # 基底层(v2) + 删除层(v3)
        self.store.compact()
        self.assertEqual(len(self.store.layers), 1)
        self.assertEqual([r["id"] for r in self.store.scan()], [2])


class NullVsNaNTest(unittest.TestCase):
    def test_null_and_nan_are_distinct(self):
        store = make_store()
        nan = float("nan")
        store.apply_batch(batch([
            {"id": 1, "balance": 1, "score": None, "name": "null-row"},
            {"id": 2, "balance": 2, "score": nan, "name": "nan-row"},
        ]))
        found_null, val_null = store.read(1, "score")
        found_nan, val_nan = store.read(2, "score")
        self.assertTrue(found_null)
        self.assertIsNone(val_null)                      # null 是 None
        self.assertTrue(found_nan)
        self.assertIsInstance(val_nan, float)
        self.assertTrue(math.isnan(val_nan))             # NaN 仍是 NaN
        self.assertFalse(values_equal(val_null, val_nan))
        self.assertFalse(values_equal(val_nan, val_null))
        self.assertTrue(values_equal(val_nan, float("nan")))
        self.assertTrue(values_equal(None, None))
        self.assertFalse(values_equal(None, 0.0))

    def test_null_not_equal_zero(self):
        store = make_store()
        store.apply_batch(batch([
            {"id": 1, "balance": None, "score": 0.0, "name": "x"},
        ]))
        found, val = store.read(1, "balance")
        self.assertTrue(found)
        self.assertIsNone(val)
        self.assertFalse(values_equal(val, 0))


class ExactIntegerTest(unittest.TestCase):
    def test_big_int_exact_arithmetic(self):
        store = make_store()
        big1 = 2**70 + 12345
        big2 = 2**69
        store.apply_batch(batch([
            {"id": 1, "balance": big1, "score": 0.0, "name": "x"},
            {"id": 2, "balance": big2, "score": 0.0, "name": "y"},
        ]))
        self.assertEqual(store.read(1, "balance"), (True, big1))
        total = store.sum_column("balance")
        self.assertIsInstance(total, int)
        self.assertEqual(total, big1 + big2)  # 任意精度精确求和

    def test_int_column_rejects_float_and_bool(self):
        store = make_store()
        with self.assertRaises(TypeError):
            store.apply_batch(batch([
                {"id": 1, "balance": 1.5, "score": 0.0, "name": "x"}]))
        with self.assertRaises(TypeError):
            store.apply_batch(batch([
                {"id": 1, "balance": True, "score": 0.0, "name": "x"}]))


class BatchValidationTest(unittest.TestCase):
    def test_schema_mismatch_rejected(self):
        store = make_store()
        bad = Batch.of({"balance": ColumnType.INT}, [{"id": 1, "balance": 1}])
        with self.assertRaises(ValueError):
            store.apply_batch(bad)

    def test_missing_column_in_row_rejected(self):
        store = make_store()
        with self.assertRaises(ValueError):
            store.apply_batch(batch([{"id": 1, "balance": 1, "score": 0.0}]))

    def test_duplicate_row_id_in_batch_rejected(self):
        store = make_store()
        row = {"id": 1, "balance": 1, "score": 0.0, "name": "x"}
        with self.assertRaises(ValueError):
            store.apply_batch(batch([row, dict(row)]))

    def test_write_and_delete_same_row_rejected(self):
        store = make_store()
        with self.assertRaises(ValueError):
            store.apply_batch(
                batch([{"id": 1, "balance": 1, "score": 0.0, "name": "x"}]),
                deletes=[1],
            )

    def test_snapshot_version_out_of_range(self):
        store = make_store()
        with self.assertRaises(ValueError):
            store.snapshot(1)
        with self.assertRaises(ValueError):
            store.read(1, "balance", 99)


class SnapshotLifecycleTest(unittest.TestCase):
    def test_closed_snapshot_raises(self):
        store = make_store()
        store.apply_batch(batch([{"id": 1, "balance": 5, "score": 0.0, "name": "x"}]))
        snap = store.snapshot()
        snap.close()
        with self.assertRaises(RuntimeError):
            snap.get(1, "balance")

    def test_context_manager_releases(self):
        store = make_store()
        store.apply_batch(batch([{"id": 1, "balance": 5, "score": 0.0, "name": "x"}]))
        with store.snapshot() as snap:
            self.assertEqual(snap.get(1, "balance"), 5)
            self.assertEqual(len(store._snapshots), 1)
        self.assertEqual(len(store._snapshots), 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
