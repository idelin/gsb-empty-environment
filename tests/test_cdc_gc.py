"""墓碑回收模拟器测试（标准库 unittest）。"""
import unittest
from pathlib import Path

from cdc_gc import (
    ChangeEvent,
    ConsumerGroup,
    GarbageCollector,
    MaterializedStore,
    MembershipError,
    Op,
    load_fixture,
)

FIXTURE_DIR = Path(__file__).resolve().parent.parent / "cdc_gc" / "fixtures"


def insert(pk, seq, table="users"):
    return ChangeEvent(f"tx-{seq}", table, pk, None, {"v": seq}, seq)


def update(pk, seq, table="users"):
    return ChangeEvent(f"tx-{seq}", table, pk, {"v": seq - 1}, {"v": seq}, seq)


def delete(pk, seq, table="users"):
    return ChangeEvent(f"tx-{seq}", table, pk, {"v": seq - 1}, None, seq)


def make_env():
    store = MaterializedStore()
    group = ConsumerGroup(lambda: store.reclaimed_horizon)
    return store, group, GarbageCollector(store, group)


class TestEventParsing(unittest.TestCase):
    def test_op_derived_from_before_after(self):
        self.assertIs(insert("u-1", 1).op, Op.INSERT)
        self.assertIs(update("u-1", 2).op, Op.UPDATE)
        self.assertIs(delete("u-1", 3).op, Op.DELETE)

    def test_both_null_rejected(self):
        with self.assertRaises(ValueError):
            ChangeEvent.from_dict(
                {"tx_id": "t", "table": "users", "pk": "u-1",
                 "before": None, "after": None, "commit_seq": 1}
            )

    def test_missing_field_rejected(self):
        with self.assertRaises(ValueError):
            ChangeEvent.from_dict({"tx_id": "t", "table": "users", "pk": "u-1"})

    def test_bad_commit_seq_rejected(self):
        for bad in (0, -1, "3", True, 1.5):
            with self.assertRaises(ValueError):
                ChangeEvent.from_dict(
                    {"tx_id": "t", "table": "users", "pk": "u-1",
                     "before": None, "after": {}, "commit_seq": bad}
                )

    def test_load_fixture(self):
        events = load_fixture(FIXTURE_DIR / "changes.jsonl")
        self.assertEqual(len(events), 5)
        self.assertEqual([e.op for e in events],
                         [Op.INSERT, Op.INSERT, Op.UPDATE, Op.DELETE, Op.DELETE])
        self.assertEqual(events[0].tx_id, "tx-1")
        self.assertEqual(events[-1].commit_seq, 5)


class TestStoreApply(unittest.TestCase):
    def test_insert_update_delete_flow(self):
        store, _, _ = make_env()
        store.apply(insert("u-1", 1))
        store.apply(update("u-1", 2))
        self.assertEqual(store.get("users", "u-1"), {"v": 2})
        result = store.apply(delete("u-1", 3))
        self.assertEqual(result.outcome, "deleted")
        self.assertIsNone(store.get("users", "u-1"))
        self.assertEqual(store.tombstone("users", "u-1").delete_seq, 3)

    def test_out_of_order_update_dropped(self):
        store, _, _ = make_env()
        store.apply(insert("u-1", 1))
        store.apply(update("u-1", 3))
        result = store.apply(update("u-1", 2))
        self.assertEqual(result.outcome, "stale")
        self.assertEqual(store.get("users", "u-1"), {"v": 3})

    def test_late_insert_blocked_by_tombstone(self):
        store, _, _ = make_env()
        store.apply(insert("u-1", 1))
        store.apply(delete("u-1", 4))
        result = store.apply(insert("u-1", 2))
        self.assertEqual(result.outcome, "blocked_by_tombstone")
        self.assertIsNone(store.get("users", "u-1"))

    def test_delete_before_insert_still_guards(self):
        store, _, _ = make_env()
        store.apply(delete("u-9", 5))  # 删除先到，实体从未出现
        result = store.apply(insert("u-9", 3))
        self.assertEqual(result.outcome, "blocked_by_tombstone")
        self.assertIsNone(store.get("users", "u-9"))

    def test_newer_insert_supersedes_tombstone(self):
        store, _, _ = make_env()
        store.apply(insert("u-1", 1))
        store.apply(delete("u-1", 4))
        result = store.apply(insert("u-1", 6))  # 更晚的插入是新一世代
        self.assertEqual(result.outcome, "applied")
        self.assertEqual(store.get("users", "u-1"), {"v": 6})
        self.assertIsNone(store.tombstone("users", "u-1"))

    def test_below_horizon_drop_after_reclaim(self):
        store, group, gc = make_env()
        store.apply(insert("u-1", 1))
        store.apply(delete("u-1", 4))
        group.register("c1", start_seq=0)
        group.advance("c1", 4)
        gc.sweep()  # 回收完成，墓碑移除、水位推进
        result = store.apply(insert("u-1", 2))
        self.assertEqual(result.outcome, "below_horizon")
        self.assertIsNone(store.get("users", "u-1"))


class TestMembership(unittest.TestCase):
    def test_explicit_register_and_deregister(self):
        _, group, _ = make_env()
        group.register("c1", start_seq=0)
        self.assertIn("c1", group.consumers)
        group.deregister("c1")
        self.assertNotIn("c1", group.consumers)
        with self.assertRaises(MembershipError):
            group.deregister("c1")

    def test_duplicate_register_rejected(self):
        _, group, _ = make_env()
        group.register("c1", start_seq=0)
        with self.assertRaises(MembershipError):
            group.register("c1", start_seq=0)

    def test_watermark_monotonic(self):
        _, group, _ = make_env()
        group.register("c1", start_seq=0)
        group.advance("c1", 5)
        with self.assertRaises(MembershipError):
            group.advance("c1", 4)
        with self.assertRaises(MembershipError):
            group.advance("ghost", 1)

    def test_new_consumer_must_start_from_safe_snapshot(self):
        store, group, _ = make_env()
        store.reclaimed_horizon = 5
        with self.assertRaises(MembershipError):
            group.register("late-joiner", start_seq=2)
        group.register("late-joiner", start_seq=5)
        self.assertEqual(group.consumers["late-joiner"].watermark, 5)

    def test_snapshot_must_respect_floor(self):
        store, group, _ = make_env()
        store.reclaimed_horizon = 3
        with self.assertRaises(MembershipError):
            group.open_snapshot("snap", seq=2)
        group.open_snapshot("snap", seq=3)
        group.close_snapshot("snap")
        with self.assertRaises(MembershipError):
            group.close_snapshot("snap")


class TestGarbageCollection(unittest.TestCase):
    def setUp(self):
        self.store, self.group, self.gc = make_env()
        self.store.apply(insert("u-1", 1))
        self.store.apply(insert("u-2", 2))
        self.store.apply(delete("u-1", 4))
        self.store.apply(delete("u-2", 5))

    def reasons_text(self, decisions):
        return "\n".join(r for d in decisions for r in d.reasons)

    def test_lagging_consumer_blocks_with_reason(self):
        self.group.register("fast", start_seq=0)
        self.group.register("slow", start_seq=0)
        self.group.advance("fast", 5)
        self.group.advance("slow", 2)
        decisions = self.gc.evaluate()
        self.assertFalse(any(d.reclaimable for d in decisions))
        text = self.reasons_text(decisions)
        self.assertIn("'slow'", text)
        self.assertIn("未越过删除点", text)
        self.assertNotIn("'fast'", text)

    def test_snapshot_reference_blocks(self):
        self.group.register("c1", start_seq=0)
        self.group.advance("c1", 5)
        self.group.open_snapshot("snap", seq=3)
        decisions = self.gc.evaluate()
        self.assertFalse(any(d.reclaimable for d in decisions))
        self.assertIn("'snap'", self.reasons_text(decisions))

    def test_reclaim_after_all_pass_and_snapshot_closed(self):
        self.group.register("c1", start_seq=0)
        self.group.open_snapshot("snap", seq=3)
        self.group.advance("c1", 5)
        self.group.close_snapshot("snap")
        reclaimed = self.gc.sweep()
        self.assertEqual({t.pk for t in reclaimed}, {"u-1", "u-2"})
        self.assertEqual(self.store.tombstones(), [])
        self.assertEqual(self.store.reclaimed_horizon, 5)

    def test_deregistered_consumer_no_longer_blocks(self):
        self.group.register("leaving", start_seq=0)
        self.group.register("staying", start_seq=0)
        self.group.advance("staying", 5)
        self.group.deregister("leaving")
        self.assertTrue(all(d.reclaimable for d in self.gc.evaluate()))

    def test_partial_reclaim_per_delete_point(self):
        self.group.register("c1", start_seq=0)
        self.group.advance("c1", 4)  # 越过 u-1@4，未越过 u-2@5
        reclaimed = self.gc.sweep()
        self.assertEqual([t.pk for t in reclaimed], ["u-1"])
        self.assertIsNotNone(self.store.tombstone("users", "u-2"))
        self.assertEqual(self.store.reclaimed_horizon, 4)
        # 回收后迟到旧插入不得复活
        result = self.store.apply(insert("u-1", 2))
        self.assertEqual(result.outcome, "below_horizon")
        self.assertIsNone(self.store.get("users", "u-1"))

    def test_late_insert_never_resurrects_full_cycle(self):
        store, group, gc = make_env()  # 独立环境，不用 setUp 的预置数据
        for event in load_fixture(FIXTURE_DIR / "changes.jsonl"):
            store.apply(event)
        group.register("c1", start_seq=0)
        group.advance("c1", 5)
        gc.sweep()
        for event in load_fixture(FIXTURE_DIR / "late_changes.jsonl"):
            store.apply(event)
        self.assertIsNone(store.get("users", "u-2"))
        self.assertEqual(len(store.dropped), 2)


if __name__ == "__main__":
    unittest.main()
