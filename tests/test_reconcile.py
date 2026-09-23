import unittest

from cdc_reconcile import (
    ChangeEvent,
    CommonWatermarkRollbackError,
    ReplicaStream,
    WatermarkTracker,
    compare,
)


def ev(seq, table="users", pk="1", after=None, before=None, txn="t1"):
    return ChangeEvent(commit_seq=seq, txn_id=txn, table=table, pk=pk,
                       before=before, after=after)


def stream(name, events):
    return ReplicaStream(name=name, events=events)


class TestCommonWatermark(unittest.TestCase):
    def test_watermark_is_min_progress(self):
        a = stream("A", [ev(1), ev(2), ev(5)])
        b = stream("B", [ev(1), ev(2), ev(3)])
        report = compare(a, b)
        self.assertEqual(report.common_watermark, 3)

    def test_lagging_side_only_reports_not_caught_up(self):
        # B 落后：A 在水位之上的事件不得被报成 B 缺事件或值分歧
        a = stream("A", [ev(1, after={"v": 1}), ev(2, after={"v": 2}),
                         ev(3, pk="2", after={"x": 1})])
        b = stream("B", [ev(1, after={"v": 1})])
        report = compare(a, b)
        self.assertEqual(report.common_watermark, 1)
        self.assertIsNotNone(report.lagging)
        self.assertEqual(report.lagging["lagging_side"], "B")
        self.assertEqual(report.lagging["pending_commit_seqs"], [2, 3])
        self.assertEqual(report.missing_events, [])
        self.assertEqual(report.value_divergences, [])
        self.assertEqual(report.same_value_missing_events, [])

    def test_no_lagging_when_progress_equal(self):
        a = stream("A", [ev(1), ev(2)])
        b = stream("B", [ev(1), ev(2)])
        report = compare(a, b)
        self.assertIsNone(report.lagging)

    def test_watermark_rollback_rejected(self):
        tracker = WatermarkTracker()
        a = stream("A", [ev(1), ev(2), ev(3)])
        b = stream("B", [ev(1), ev(2), ev(3)])
        compare(a, b, tracker=tracker)
        self.assertEqual(tracker.current, 3)
        c = stream("C", [ev(1)])
        d = stream("D", [ev(1), ev(2)])
        with self.assertRaises(CommonWatermarkRollbackError):
            compare(c, d, tracker=tracker)
        self.assertEqual(tracker.current, 3)  # 回退失败，水位不变

    def test_watermark_advance_equal_allowed(self):
        tracker = WatermarkTracker()
        tracker.advance(5)
        self.assertEqual(tracker.advance(5), 5)


class TestMissingEvents(unittest.TestCase):
    def test_missing_event_within_watermark(self):
        a = stream("A", [ev(1, after={"v": 1}), ev(2, after={"v": 2})])
        b = stream("B", [ev(1, after={"v": 1}), ev(9, pk="z", after={"v": 0})])
        # B 进度 9，水位 = 2；B 缺事件 2
        report = compare(a, b)
        self.assertEqual(report.common_watermark, 2)
        self.assertEqual(len(report.missing_events), 1)
        self.assertEqual(report.missing_events[0]["missing_on_B"], [2])

    def test_same_final_value_with_missing_events_listed_separately(self):
        # 两侧最终值相同，但 B 缺了中间事件：单列，不算值分歧
        a = stream("A", [ev(1, after={"v": 1}), ev(2, after={"v": 1})])
        b = stream("B", [ev(1, after={"v": 1}), ev(9, pk="z", after={"v": 0})])
        report = compare(a, b)
        self.assertEqual(report.value_divergences, [])
        self.assertEqual(len(report.same_value_missing_events), 1)
        item = report.same_value_missing_events[0]
        self.assertEqual((item["table"], item["pk"]), ("users", "1"))
        self.assertEqual(item["final_value"], {"v": 1})
        self.assertEqual(item["missing_on_B"], [2])

    def test_entity_only_on_one_side_is_missing_not_divergence(self):
        a = stream("A", [ev(1, pk="1", after={"v": 1}), ev(2, pk="2", after={"v": 2})])
        b = stream("B", [ev(1, pk="1", after={"v": 1}), ev(9, pk="z", after={"v": 0})])
        report = compare(a, b)
        self.assertEqual(len(report.missing_events), 1)
        self.assertEqual(report.missing_events[0]["pk"], "2")
        self.assertEqual(report.value_divergences, [])


class TestOrderingConflict(unittest.TestCase):
    def test_same_events_different_order(self):
        a = stream("A", [ev(1, after={"v": 1}), ev(2, after={"v": 2})])
        b = stream("B", [ev(2, after={"v": 2}), ev(1, after={"v": 1})])
        report = compare(a, b)
        self.assertEqual(len(report.ordering_conflicts), 1)
        conflict = report.ordering_conflicts[0]
        self.assertEqual(conflict["order_A"], [1, 2])
        self.assertEqual(conflict["order_B"], [2, 1])
        # 顺序不同导致最终值不同，同时报值分歧
        self.assertEqual(len(report.value_divergences), 1)

    def test_same_order_no_conflict(self):
        a = stream("A", [ev(1, after={"v": 1}), ev(2, after={"v": 2})])
        b = stream("B", [ev(1, after={"v": 1}), ev(2, after={"v": 2})])
        report = compare(a, b)
        self.assertEqual(report.ordering_conflicts, [])
        self.assertEqual(report.consistent_entities, 1)


class TestValueDivergence(unittest.TestCase):
    def test_divergent_final_value(self):
        a = stream("A", [ev(1, after={"v": 1})])
        b = stream("B", [ev(1, after={"v": 9})])
        report = compare(a, b)
        self.assertEqual(len(report.value_divergences), 1)
        div = report.value_divergences[0]
        self.assertEqual(div["value_A"], {"v": 1})
        self.assertEqual(div["value_B"], {"v": 9})

    def test_delete_vs_value_is_divergence(self):
        a = stream("A", [ev(1, after=None)])
        b = stream("B", [ev(1, after={"v": 1})])
        report = compare(a, b)
        self.assertEqual(len(report.value_divergences), 1)


class TestFixtureSample(unittest.TestCase):
    def test_fixture_repositories(self):
        a = ReplicaStream.from_file("fixtures/replica_a.json")
        b = ReplicaStream.from_file("fixtures/replica_b.json")
        report = compare(a, b)

        self.assertEqual(report.common_watermark, 108)
        self.assertEqual(report.lagging["lagging_side"], "B")
        self.assertEqual(report.lagging["pending_commit_seqs"], [109, 110])

        # 水位内缺事件：B 缺 99（users:5）和 104（users:2）
        missing = {(m["table"], m["pk"]): m for m in report.missing_events}
        self.assertEqual(missing[("users", "5")]["missing_on_B"], [99])
        self.assertEqual(missing[("users", "2")]["missing_on_B"], [104])

        # 顺序冲突：orders:9（并引发值分歧）、orders:10（同值）
        conflicts = {(c["table"], c["pk"]) for c in report.ordering_conflicts}
        self.assertEqual(conflicts, {("orders", "9"), ("orders", "10")})

        # 值分歧：orders:9（顺序颠倒）、users:2（缺 104）
        divergences = {(d["table"], d["pk"]) for d in report.value_divergences}
        self.assertEqual(divergences, {("orders", "9"), ("users", "2")})

        # 同值缺事件：users:5 最终值相同但 B 缺 99
        self.assertEqual(len(report.same_value_missing_events), 1)
        self.assertEqual(report.same_value_missing_events[0]["pk"], "5")

        # 水位之上的 109/110 不得出现在任何数据错误里
        for entry in report.missing_events:
            for key, value in entry.items():
                if key.startswith("missing_on_"):
                    self.assertNotIn(109, value)
                    self.assertNotIn(110, value)


if __name__ == "__main__":
    unittest.main()
