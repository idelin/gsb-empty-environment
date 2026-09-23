"""一致性快照同步的单元测试（标准库 unittest）。"""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from cdc_sync import (
    OP_DELETE,
    OP_INSERT,
    OP_UPDATE,
    ChangeCache,
    ChangeEvent,
    SnapshotSynchronizer,
    SourceDatabase,
    TargetDatabase,
)
from cdc_sync.fixture import group_transactions, load_fixture

FIXTURE = Path(__file__).resolve().parent.parent / "fixtures" / "changes.json"


def ev(tx_id, table, pk, op, before=None, after=None, seq=None):
    return ChangeEvent(tx_id=tx_id, table=table, pk=pk, op=op,
                       before=before, after=after, commit_seq=seq)


class Base(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        root = Path(self._tmp.name)
        self.source = SourceDatabase(str(root / "source.db"))
        self.target = TargetDatabase(str(root / "target.db"))
        self.sync = SnapshotSynchronizer(self.source, self.target)

    def tearDown(self):
        self.source.close()
        self.target.close()
        self._tmp.cleanup()

    def commit(self, events):
        """把一批事件按 tx_id 分组逐事务提交到源库，返回分配 seq 后的事件。"""
        assigned = []
        for tx in group_transactions(events):
            assigned.extend(self.source.apply_transaction(tx))
        return assigned

    def seed(self):
        """3 个用户 + 1 张订单，commit_seq 1..4。"""
        return self.commit([
            ev("t1", "users", "u1", OP_INSERT, after={"name": "A", "bal": 100}),
            ev("t2", "users", "u2", OP_INSERT, after={"name": "B", "bal": 50}),
            ev("t3", "users", "u3", OP_INSERT, after={"name": "C", "bal": 30}),
            ev("t4", "orders", "o1", OP_INSERT, after={"user": "u1", "amt": 88}),
        ])


class TestSnapshotConsistency(Base):
    def test_watermark_and_rows_come_from_same_view(self):
        self.seed()
        snapshot = self.source.open_snapshot()
        # 快照打开后再提交新变更，读者不应看到。
        self.commit([ev("t5", "users", "u9", OP_INSERT, after={"name": "X", "bal": 1})])
        self.assertEqual(snapshot.watermark, 4)
        rows = {pk: data for pk, data in snapshot.iter_rows("users")}
        self.assertEqual(set(rows), {"u1", "u2", "u3"})
        snapshot.close()

    def test_concurrent_writes_during_scan_do_not_leak_into_snapshot(self):
        self.seed()
        seen = []

        def during_scan(_n):
            if not seen:
                self.commit([
                    ev("t5", "users", "u1", OP_UPDATE,
                       before={"name": "A", "bal": 100}, after={"name": "A", "bal": 999}),
                    ev("t6", "users", "u4", OP_INSERT, after={"name": "D", "bal": 77}),
                ])
            seen.append(1)

        report = self.sync.run(during_scan=during_scan)
        self.assertEqual(report.watermark, 4)
        self.assertEqual(report.snapshot_rows, 4)  # 快照只含水位时的 4 行
        # 最终状态必须追上源库最新值。
        self.assertEqual(self.target.live_rows("users")["u1"], {"name": "A", "bal": 999})
        self.assertEqual(self.target.live_rows("users")["u4"], {"name": "D", "bal": 77})


class TestReplaySemantics(Base):
    def test_update_after_watermark_applied_exactly_once(self):
        self.seed()

        def during_scan(_n):
            if _n == 1:
                self.commit([ev("t5", "users", "u1", OP_UPDATE,
                                before={"name": "A", "bal": 100},
                                after={"name": "A", "bal": 999})])

        report = self.sync.run(during_scan=during_scan)
        self.assertEqual(report.applied, 1)
        self.assertEqual(self.target.live_rows("users")["u1"]["bal"], 999)

    def test_delete_during_scan_is_deleted_finally(self):
        self.seed()

        def during_scan(_n):
            if _n == 1:
                self.commit([ev("t5", "users", "u2", OP_DELETE,
                                before={"name": "B", "bal": 50})])

        self.sync.run(during_scan=during_scan)
        self.assertNotIn("u2", self.target.live_rows("users"))
        self.assertIn(("users", "u2"), self.target.live_tombstones())

    def test_insert_then_delete_after_watermark_leaves_nothing(self):
        self.seed()
        self.commit([
            ev("t5", "orders", "o2", OP_INSERT, after={"user": "u3", "amt": 12}),
            ev("t6", "orders", "o2", OP_DELETE, before={"user": "u3", "amt": 12}),
        ])
        self.sync.run()
        self.assertNotIn("o2", self.target.live_rows("orders"))

    def test_event_at_or_below_watermark_is_not_reapplied(self):
        self.seed()
        report = self.sync.run()
        self.assertEqual(report.watermark, 4)
        # 缓存层：commit_seq <= 水位的事件直接拒绝。
        cache = ChangeCache(watermark=4)
        stale = ev("t4", "orders", "o1", OP_INSERT,
                   after={"user": "u1", "amt": 88}, seq=4)
        self.assertFalse(cache.add(stale))
        self.assertEqual(len(cache), 0)
        # 目标层：对快照行（seq=水位）重放旧事件被幂等跳过。
        self.target.reset_staging()
        self.target.ensure_staging_table("orders")
        self.target.stage_row("orders", "o1", {"user": "u1", "amt": 88}, seq=4)
        self.assertFalse(self.target.apply_event(stale))
        self.assertFalse(self.target.apply_event(
            ev("t3", "orders", "o1", OP_UPDATE,
               before={"user": "u1", "amt": 88},
               after={"user": "u1", "amt": 1}, seq=3)))

    def test_replay_is_idempotent_and_tombstone_blocks_resurrection(self):
        self.target.ensure_staging_table("users")
        update = ev("t1", "users", "u1", OP_UPDATE,
                    before={"bal": 1}, after={"bal": 2}, seq=10)
        self.assertTrue(self.target.apply_event(update))
        self.assertFalse(self.target.apply_event(update))  # 重复投递不再应用
        delete = ev("t2", "users", "u1", OP_DELETE, before={"bal": 2}, seq=11)
        self.assertTrue(self.target.apply_event(delete))
        # 乱序到达的旧更新不得复活已删除的行。
        self.assertFalse(self.target.apply_event(update))
        self.assertFalse(self.target.apply_event(delete))  # 删除同样幂等

    def test_duplicate_delivery_deduped_in_cache(self):
        cache = ChangeCache(watermark=1)
        event = ev("t2", "users", "u1", OP_INSERT, after={"bal": 1}, seq=2)
        self.assertTrue(cache.add(event))
        self.assertFalse(cache.add(event))
        self.assertEqual(len(cache), 1)
        self.assertEqual(cache.dropped, 1)

    def test_final_state_matches_source(self):
        self.seed()
        self.commit([
            ev("t5", "users", "u1", OP_UPDATE,
               before={"name": "A", "bal": 100}, after={"name": "A", "bal": 999}),
            ev("t6", "users", "u2", OP_DELETE, before={"name": "B", "bal": 50}),
            ev("t7", "users", "u4", OP_INSERT, after={"name": "D", "bal": 77}),
        ])
        self.sync.run()
        for tbl in self.source.tables():
            self.assertEqual(self.target.live_rows(tbl), self.source.table_rows(tbl))


class TestAtomicPublish(Base):
    def test_failed_snapshot_publishes_nothing_on_empty_target(self):
        self.seed()

        def boom(_n):
            raise RuntimeError("snapshot read failed")

        with self.assertRaises(RuntimeError):
            self.sync.run(during_scan=boom)
        self.assertEqual(self.target.live_tables(), [])

    def test_failed_resync_keeps_previous_generation(self):
        self.seed()
        self.sync.run()
        before = {t: self.target.live_rows(t) for t in self.target.live_tables()}
        self.commit([ev("t5", "users", "u1", OP_UPDATE,
                        before={"name": "A", "bal": 100},
                        after={"name": "A", "bal": 999})])

        def boom(_n):
            raise RuntimeError("snapshot read failed")

        with self.assertRaises(RuntimeError):
            self.sync.run(during_scan=boom)
        after = {t: self.target.live_rows(t) for t in self.target.live_tables()}
        self.assertEqual(before, after)  # 仍是旧一代，无混合状态

    def test_publish_failure_rolls_back_to_old_generation(self):
        self.seed()
        self.sync.run()
        before = {t: self.target.live_rows(t) for t in self.target.live_tables()}
        self.commit([ev("t5", "users", "u1", OP_UPDATE,
                        before={"name": "A", "bal": 100},
                        after={"name": "A", "bal": 999})])
        original_publish = self.target.publish

        def broken_publish():
            raise RuntimeError("publish crashed")

        self.target.publish = broken_publish
        try:
            with self.assertRaises(RuntimeError):
                self.sync.run()
        finally:
            self.target.publish = original_publish
        after = {t: self.target.live_rows(t) for t in self.target.live_tables()}
        self.assertEqual(before, after)


class TestFixture(Base):
    def test_fixture_loads_and_carries_required_fields(self):
        fixture = load_fixture(FIXTURE)
        self.assertIn("pre_snapshot", fixture)
        self.assertIn("during_scan", fixture)
        for events in fixture.values():
            for event in events:
                self.assertTrue(event.tx_id)
                self.assertTrue(event.table)
                self.assertTrue(event.pk)
                self.assertIn(event.op, {OP_INSERT, OP_UPDATE, OP_DELETE})
                self.assertIsNone(event.commit_seq)  # 提交时由源库分配

    def test_fixture_scenario_end_to_end(self):
        fixture = load_fixture(FIXTURE)
        self.commit(fixture["pre_snapshot"])
        pending = group_transactions(fixture["during_scan"])

        def during_scan(_n):
            for _ in range(2):
                if pending:
                    self.source.apply_transaction(pending.pop(0))

        report = self.sync.run(during_scan=during_scan)
        self.assertEqual(pending, [])  # 全部在扫描期间注入
        self.assertEqual(report.watermark, 5)
        self.assertEqual(report.cached_events, 5)
        users = self.target.live_rows("users")
        self.assertEqual(users["u1"]["balance"], 999)  # 水位后更新生效
        self.assertNotIn("u2", users)                   # 扫描期删除最终删除
        self.assertEqual(users["u4"]["balance"], 77)   # 扫描期插入生效
        orders = self.target.live_rows("orders")
        self.assertNotIn("o2", orders)                 # 插入后又删除 -> 不存在
        for tbl in self.source.tables():
            self.assertEqual(self.target.live_rows(tbl), self.source.table_rows(tbl))


if __name__ == "__main__":
    unittest.main()
