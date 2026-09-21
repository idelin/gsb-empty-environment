"""Debouncer 的可复现事件回放测试。

所有时间推进均由测试显式驱动（ingest / advance_to），不使用真实 sleep。
"""

import tempfile
import unittest
from pathlib import Path

from debouncer import UNKNOWN, Change, Debouncer

THRESHOLD = 3


class DebouncerSemanticsTest(unittest.TestCase):
    def test_spec_scenario_confirms_b(self):
        """门限 3：A@0、B@1、B@4 应确认 B（确认时间为 1+3=4）。"""
        deb = Debouncer(THRESHOLD)
        deb.ingest("s", "A", 0)
        deb.ingest("s", "B", 1)
        deb.ingest("s", "B", 4)
        self.assertEqual(deb.drain(), [Change("s", "B", 4.0)])
        self.assertEqual(deb.drain(), [])  # 只投递一次

    def test_unknown_interrupts_candidate(self):
        """门限 3：A@0、B@1、未知@3，则 B 不能在 4 确认。"""
        deb = Debouncer(THRESHOLD)
        deb.ingest("s", "A", 0)
        deb.ingest("s", "B", 1)
        deb.ingest("s", UNKNOWN, 3)
        deb.advance_to(10)
        self.assertEqual(deb.drain(), [])
        # 未知之后的新候选必须重新累计完整门限
        deb.ingest("s", "B", 11)
        deb.advance_to(13)
        self.assertEqual(deb.drain(), [])
        deb.advance_to(14)
        self.assertEqual(deb.drain(), [Change("s", "B", 14.0)])

    def test_threshold_boundary_is_inclusive(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest("s", "B", 1)
        deb.advance_to(3.999)
        self.assertEqual(deb.drain(), [])
        deb.advance_to(4)
        self.assertEqual(deb.drain(), [Change("s", "B", 4.0)])

    def test_duplicate_events_do_not_extend_timer(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest("s", "B", 1)
        deb.ingest("s", "B", 2)
        deb.ingest("s", "B", 3)
        deb.advance_to(4)  # 若重复事件重置计时，则要到 6 才确认
        self.assertEqual(deb.drain(), [Change("s", "B", 4.0)])

    def test_sources_are_isolated(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest("s1", "A", 0)
        deb.ingest("s2", "X", 0)
        deb.ingest("s1", "B", 1)  # 打断 s1 的 A，不影响 s2
        deb.advance_to(3)
        self.assertEqual(deb.drain(), [Change("s2", "X", 3.0)])
        deb.advance_to(4)
        self.assertEqual(deb.drain(), [Change("s1", "B", 4.0)])

    def test_same_timestamp_last_write_wins(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest("s", "B", 1)
        deb.ingest("s", "C", 1)  # 同一时刻后到覆盖先到
        deb.advance_to(4)
        self.assertEqual(deb.drain(), [Change("s", "C", 4.0)])

    def test_confirmation_settles_before_events_at_same_ts(self):
        """t=4 时先确认到期候选 B，再应用新事件 C。"""
        deb = Debouncer(THRESHOLD)
        deb.ingest("s", "B", 1)
        deb.ingest("s", "C", 4)
        self.assertEqual(deb.drain(), [Change("s", "B", 4.0)])
        deb.advance_to(7)
        self.assertEqual(deb.drain(), [Change("s", "C", 7.0)])

    def test_revert_to_confirmed_cancels_pending_candidate(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest("s", "B", 0)
        deb.advance_to(3)
        self.assertEqual(deb.drain(), [Change("s", "B", 3.0)])
        deb.ingest("s", "C", 4)
        deb.ingest("s", "B", 5)  # 回退到已确认值：取消 C，且不再重发 B
        deb.advance_to(10)
        self.assertEqual(deb.drain(), [])

    def test_late_event_rejected(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest("s", "A", 5)
        with self.assertRaises(ValueError):
            deb.ingest("s", "B", 4)
        with self.assertRaises(ValueError):
            deb.advance_to(4)


def replay(ops, tmpdir, checkpoint_after=()):
    """回放操作脚本，可选择在指定步骤后 save+restore。

    ops: ("ingest", source, value, ts) | ("advance", ts) | ("drain",)
    返回每次 drain 的结果列表。
    """
    deb = Debouncer(THRESHOLD)
    drains = []
    for step, op in enumerate(ops):
        if op[0] == "ingest":
            deb.ingest(op[1], op[2], op[3])
        elif op[0] == "advance":
            deb.advance_to(op[1])
        elif op[0] == "drain":
            drains.append(deb.drain())
        if step in checkpoint_after:
            path = Path(tmpdir) / f"checkpoint-{step}.json"
            deb.save(path)
            deb = Debouncer.restore(path)
    return drains


class CheckpointRestoreTest(unittest.TestCase):
    """把 checkpoint 分别放在三个关键位置，逐一验证恢复结果。"""

    # 步骤索引: 0:A@0  1:B@1  2:advance(3)  3:B@4  4:drain  5:advance(10)  6:drain
    OPS = [
        ("ingest", "s", "A", 0),
        ("ingest", "s", "B", 1),
        ("advance", 3),           # 即将确认（截止时间为 4）
        ("ingest", "s", "B", 4),  # 触发确认
        ("drain",),
        ("advance", 10),
        ("drain",),
    ]
    EXPECTED = [[Change("s", "B", 4.0)], []]

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmpdir = self._tmp.name

    def test_reference_run_without_checkpoint(self):
        self.assertEqual(replay(self.OPS, self.tmpdir), self.EXPECTED)

    def test_checkpoint_right_after_candidate_started(self):
        """checkpoint 放在候选刚开始（B@1 之后）。"""
        drains = replay(self.OPS, self.tmpdir, checkpoint_after={1})
        self.assertEqual(drains, self.EXPECTED)

    def test_checkpoint_just_before_confirmation(self):
        """checkpoint 放在即将确认（advance 到 3 之后，截止时间为 4）。"""
        drains = replay(self.OPS, self.tmpdir, checkpoint_after={2})
        self.assertEqual(drains, self.EXPECTED)

    def test_checkpoint_after_confirm_before_consume(self):
        """checkpoint 放在确认已生成但尚未消费（B@4 之后、drain 之前）。"""
        drains = replay(self.OPS, self.tmpdir, checkpoint_after={3})
        self.assertEqual(drains, self.EXPECTED)

    def test_checkpoint_at_every_step_is_equivalent(self):
        """每一步都 save+restore，结果与直跑完全一致。"""
        drains = replay(self.OPS, self.tmpdir, checkpoint_after=set(range(len(self.OPS))))
        self.assertEqual(drains, self.EXPECTED)

    def test_restore_does_not_reemit_or_lose(self):
        """恢复后：不重发已确认变化，不丢未满足时长的候选。"""
        deb = Debouncer(THRESHOLD)
        deb.ingest("s2", "X", 0)
        deb.ingest("s1", "B", 1)   # s1: 候选 B，截止 4
        deb.advance_to(3)          # s2 的 X 确认，未消费
        path = Path(self.tmpdir) / "cp.json"
        deb.save(path)

        restored = Debouncer.restore(path)
        # 未消费的确认不丢失，且只投递一次
        self.assertEqual(restored.drain(), [Change("s2", "X", 3.0)])
        self.assertEqual(restored.drain(), [])
        # 未满足时长的候选继续累计剩余时长
        restored.advance_to(4)
        self.assertEqual(restored.drain(), [Change("s1", "B", 4.0)])
        # 已确认的变化不会因时间推进或旧事件重放而再次发送
        restored.advance_to(100)
        restored.ingest("s1", "B", 100)
        restored.advance_to(200)
        self.assertEqual(restored.drain(), [])

    def test_snapshot_content_is_json_and_complete(self):
        import json

        deb = Debouncer(THRESHOLD)
        deb.ingest("s", "B", 1)
        path = Path(self.tmpdir) / "cp.json"
        deb.save(path)
        payload = json.loads(path.read_text(encoding="utf-8"))
        self.assertEqual(payload["threshold"], float(THRESHOLD))
        self.assertEqual(payload["now"], 1.0)
        self.assertEqual(
            payload["sources"]["s"],
            {
                "has_confirmed": False,
                "confirmed": None,
                "candidate": "B",
                "candidate_since": 1.0,
            },
        )
        self.assertEqual(payload["outbox"], [])


if __name__ == "__main__":
    unittest.main()
