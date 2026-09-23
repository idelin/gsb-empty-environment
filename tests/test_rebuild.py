"""重建-追平-切换全链路测试（标准库 unittest）。"""

from __future__ import annotations

import threading
import unittest
from pathlib import Path

from cdc_view import (
    BaseStore,
    FrozenViewError,
    MaterializedView,
    ViewManager,
    load_events,
    load_snapshot,
)

FIXTURES = Path(__file__).resolve().parent.parent / "fixtures"

# 手工推算的最终聚合（独立于实现，用于校验无双计）：
# orders:    3/600 -> +T1(400) -> T2(+50) -> -T4(300) -> +T6(150) = 4/900
# customers: 2/120 -> +T3(30) -> T5(+30)                          = 3/180
EXPECTED_FINAL = {
    "orders": {"count": 4, "amount": 900},
    "customers": {"count": 3, "amount": 180},
}


def make_env():
    tables, watermark = load_snapshot(FIXTURES / "snapshot.json")
    events = load_events(FIXTURES / "changes.json")
    store = BaseStore(tables, watermark)
    manager = ViewManager()
    manager.bootstrap(tables, watermark)
    return store, manager, events, tables, watermark


class BootstrapTest(unittest.TestCase):
    def test_bootstrap_stats_match_snapshot(self):
        _, manager, _, _, watermark = make_env()
        view = manager.acquire()
        self.assertEqual(view.version, 1)
        self.assertEqual(view.watermark, watermark)
        self.assertEqual(
            view.stats(),
            {
                "orders": {"count": 3, "amount": 600},
                "customers": {"count": 2, "amount": 120},
            },
        )

    def test_incremental_apply_without_rebuild(self):
        _, manager, events, _, _ = make_env()
        for event in events:
            manager.apply_change(event)
        view = manager.acquire()
        self.assertEqual(view.version, 1)  # 无重建，版本不变
        self.assertEqual(view.stats(), EXPECTED_FINAL)
        self.assertEqual(view.watermark, events[-1].commit_seq)


class RebuildSwitchTest(unittest.TestCase):
    def test_rebuild_publishes_only_after_caught_up(self):
        store, manager, events, _, _ = make_env()
        # 重建前先到 2 条，使固定快照与 v1 产生差异
        for event in events[:2]:
            store.apply(event)
            manager.apply_change(event)
        snap_tables, snap_wm = store.snapshot()
        self.assertEqual(snap_wm, 102)

        gate = threading.Event()
        thread = manager.start_rebuild(
            snap_tables, snap_wm, build_hook=lambda: gate.wait(5)
        )
        # 构建期间：增量进缓存，版本不得切换（未追平不发布）
        for event in events[2:]:
            store.apply(event)
            manager.apply_change(event)
        self.assertEqual(manager.current_version(), 1)
        self.assertTrue(manager.rebuild_in_progress)

        gate.set()
        thread.join(5)
        self.assertFalse(thread.is_alive())
        self.assertEqual(manager.last_rebuild_status, "PUBLISHED")
        self.assertEqual(manager.current_version(), 2)
        view = manager.acquire()
        self.assertEqual(view.watermark, events[-1].commit_seq)  # 已追平
        self.assertEqual(view.stats(), EXPECTED_FINAL)

    def test_no_double_count_across_switch(self):
        # 夹具含 T3/T6 重复投递；切换后结果须等于手工推算值
        store, manager, events, _, _ = make_env()
        manager.start_rebuild(*store.snapshot())
        for event in events:
            store.apply(event)
            manager.apply_change(event)
        view = manager.acquire()
        self.assertEqual(view.stats(), EXPECTED_FINAL)
        # 切换后再重复投递一次，仍不得双计
        for event in events:
            manager.apply_change(event)
        self.assertEqual(manager.acquire().stats(), EXPECTED_FINAL)

    def test_old_reader_keeps_old_version(self):
        store, manager, events, _, _ = make_env()
        old_reader = manager.acquire()
        gate = threading.Event()
        thread = manager.start_rebuild(
            *store.snapshot(), build_hook=lambda: gate.wait(5)
        )
        for event in events:
            store.apply(event)
            manager.apply_change(event)
        gate.set()
        thread.join(5)
        self.assertEqual(manager.current_version(), 2)
        # 旧读者句柄仍是 v1，已冻结但可读
        self.assertEqual(old_reader.version, 1)
        self.assertTrue(old_reader.frozen)
        self.assertEqual(old_reader.stats(), EXPECTED_FINAL)  # 冻结前已是最新
        with self.assertRaises(FrozenViewError):
            old_reader.apply(events[0])
        # 新读者拿到 v2
        self.assertEqual(manager.acquire().version, 2)

    def test_stale_snapshot_never_publishes(self):
        # 快照水位落后于已应用增量，且缺失增量不在缓冲区 -> 永远追不平
        _, manager, events, tables, watermark = make_env()
        for event in events[:2]:  # 101/102 已进旧视图，但不进缓冲区
            manager.apply_change(event)
        thread = manager.start_rebuild(tables, watermark)  # 过旧快照 @100
        thread.join(5)
        self.assertEqual(manager.current_version(), 1)  # 未发布
        self.assertTrue(manager.rebuild_in_progress)
        manager.cancel_rebuild()
        self.assertFalse(manager.rebuild_in_progress)


class FailureTest(unittest.TestCase):
    def test_failed_rebuild_keeps_old_view(self):
        store, manager, events, _, _ = make_env()
        for event in events:
            store.apply(event)
            manager.apply_change(event)
        before_stats = manager.acquire().stats()
        thread = manager.start_rebuild(*store.snapshot(), fail=True)
        thread.join(5)
        self.assertEqual(manager.last_rebuild_status, "FAILED")
        self.assertEqual(manager.current_version(), 1)  # 旧视图仍在
        self.assertEqual(manager.acquire().stats(), before_stats)
        # 失败后旧视图继续接受增量
        extra = load_events(FIXTURES / "changes.json")[0]
        manager.apply_change(extra)  # 重复投递，幂等跳过
        self.assertEqual(manager.acquire().stats(), before_stats)
        # 后续正常重建仍可成功
        manager.start_rebuild(*store.snapshot()).join(5)
        self.assertEqual(manager.last_rebuild_status, "PUBLISHED")
        self.assertEqual(manager.current_version(), 2)

    def test_concurrent_rebuild_rejected(self):
        store, manager, _, _, _ = make_env()
        gate = threading.Event()
        manager.start_rebuild(*store.snapshot(), build_hook=lambda: gate.wait(5))
        from cdc_view import RebuildError

        with self.assertRaises(RebuildError):
            manager.start_rebuild(*store.snapshot())
        gate.set()


class ViewSemanticsTest(unittest.TestCase):
    def test_apply_out_of_order_or_stale_event_skipped(self):
        view = MaterializedView.from_snapshot(1, {"t": []}, watermark=10)
        from cdc_view import ChangeEvent

        stale = ChangeEvent("T0", "t", 1, None, {"id": 1, "amount": 5}, 9)
        self.assertFalse(view.apply(stale))  # 已被快照覆盖
        fresh = ChangeEvent("T1", "t", 1, None, {"id": 1, "amount": 5}, 11)
        self.assertTrue(view.apply(fresh))
        self.assertFalse(view.apply(fresh))  # 重投幂等
        self.assertEqual(view.stats(), {"t": {"count": 1, "amount": 5}})


if __name__ == "__main__":
    unittest.main()
