"""可复现的事件回放测试：所有时间推进均由测试显式驱动，不使用真实 sleep。

回放脚本指令：
    ("ingest", source, value, ts)  摄入事件
    ("advance", now)               推进事件时间
    ("drain",)                     消费已确认变化（记录到输出）
    ("checkpoint",)                save -> JSON 往返 -> restore，继续回放
"""

import json
import unittest

from debouncer import Change, Debouncer


def replay(script, threshold=3):
    """回放脚本，返回所有 drain 出的 Change 列表（按发生顺序）。"""
    debouncer = Debouncer(threshold)
    outputs = []
    for op in script:
        if op[0] == "ingest":
            _, source, value, ts = op
            debouncer.ingest(source, value, ts)
        elif op[0] == "advance":
            debouncer.advance(op[1])
        elif op[0] == "drain":
            outputs.extend(debouncer.drain())
        elif op[0] == "checkpoint":
            debouncer = Debouncer.restore(json.loads(json.dumps(debouncer.save())))
        else:
            raise AssertionError(f"未知指令: {op!r}")
    return outputs


class ThresholdSemanticsTest(unittest.TestCase):
    def test_confirm_after_full_threshold(self):
        # 门限 3：A@0、B@1、B@4 —— B 从 1 起稳定，推进到 4 恰好满 3，确认 B
        out = replay([
            ("ingest", "s", "A", 0),
            ("ingest", "s", "B", 1),
            ("ingest", "s", "B", 4),
            ("advance", 3.999),
            ("drain",),
            ("advance", 4),
            ("drain",),
        ])
        self.assertEqual(out, [Change("s", "B", 4)])

    def test_unknown_value_interrupts_candidate(self):
        # 同一序列但 3 时刻出现未知值：候选被中断，4 时刻不能确认
        out = replay([
            ("ingest", "s", "A", 0),
            ("ingest", "s", "B", 1),
            ("ingest", "s", None, 3),
            ("ingest", "s", "B", 4),   # 未知值之后 B 重新成为候选，从 4 重新计时
            ("advance", 4),
            ("drain",),
            ("advance", 6.999),
            ("drain",),
            ("advance", 7),            # 4 + 3，此时才确认
            ("drain",),
        ])
        self.assertEqual(out, [Change("s", "B", 7)])

    def test_duplicate_events_do_not_extend_timer(self):
        # B@2、B@3.5 是重复事件，稳定计时仍从 1 起算，4 即确认而非 6.5
        out = replay([
            ("ingest", "s", "A", 0),
            ("ingest", "s", "B", 1),
            ("ingest", "s", "B", 2),
            ("ingest", "s", "B", 3.5),
            ("advance", 4),
            ("drain",),
        ])
        self.assertEqual(out, [Change("s", "B", 4)])

    def test_same_timestamp_events_apply_in_ingest_order(self):
        # 同一时刻多事件按摄入顺序生效，最后切换者成为候选
        out = replay([
            ("ingest", "s", "A", 0),
            ("ingest", "s", "B", 0),
            ("ingest", "s", "C", 0),
            ("advance", 3),
            ("drain",),
        ])
        self.assertEqual(out, [Change("s", "C", 3)])

    def test_revert_to_confirmed_value_emits_nothing(self):
        out = replay([
            ("ingest", "s", "A", 0),
            ("advance", 3),
            ("drain",),                # A 已确认
            ("ingest", "s", "B", 4),
            ("ingest", "s", "A", 5),   # 回到已确认值：候选清除
            ("advance", 100),
            ("drain",),
        ])
        self.assertEqual(out, [Change("s", "A", 3)])

    def test_sources_are_isolated(self):
        out = replay([
            ("ingest", "s1", "A", 0),
            ("ingest", "s2", "X", 0),
            ("ingest", "s2", "Y", 2),  # 只影响 s2
            ("ingest", "s1", None, 1), # 只中断 s1
            ("ingest", "s1", "A", 1.5),
            ("advance", 5),            # s2: Y 自 2 起满 3；s1: A 自 1.5 起 3.5 也满
            ("drain",),
        ])
        # 同一 advance 内按来源首次出现顺序入队
        self.assertEqual(out, [Change("s1", "A", 5), Change("s2", "Y", 5)])

    def test_zero_threshold_confirms_on_next_advance(self):
        out = replay([("ingest", "s", "A", 0), ("advance", 0), ("drain",)], threshold=0)
        self.assertEqual(out, [Change("s", "A", 0)])

    def test_late_or_rewound_events_rejected(self):
        d = Debouncer(3)
        d.ingest("s", "A", 2)
        with self.assertRaises(ValueError):
            d.ingest("s", "B", 1)      # 单源时间回退
        d.advance(5)
        with self.assertRaises(ValueError):
            d.ingest("s", "B", 4)      # 早于已推进时间
        with self.assertRaises(ValueError):
            d.advance(4)               # 推进时间回退


class CheckpointTest(unittest.TestCase):
    """在三个关键位置打 checkpoint（含 JSON 往返），逐一验证恢复结果。"""

    BASE = [
        ("ingest", "s", "A", 0),
        ("ingest", "s", "B", 1),
        ("ingest", "s", "B", 4),
        ("advance", 4),
        ("drain",),
    ]
    EXPECTED = [Change("s", "B", 4)]

    def test_checkpoint_when_candidate_just_started(self):
        script = [
            ("ingest", "s", "A", 0),
            ("ingest", "s", "B", 1),
            ("checkpoint",),           # 候选刚开始
            ("ingest", "s", "B", 4),
            ("advance", 4),
            ("drain",),
        ]
        self.assertEqual(replay(script), self.EXPECTED)

    def test_checkpoint_just_before_confirmation(self):
        script = [
            ("ingest", "s", "A", 0),
            ("ingest", "s", "B", 1),
            ("advance", 3.999),        # 即将确认
            ("checkpoint",),
            ("advance", 4),
            ("drain",),
        ]
        self.assertEqual(replay(script), self.EXPECTED)

    def test_checkpoint_after_confirm_before_consume(self):
        script = [
            ("ingest", "s", "A", 0),
            ("ingest", "s", "B", 1),
            ("ingest", "s", "B", 4),
            ("advance", 4),            # 确认已生成
            ("checkpoint",),           # 但尚未消费
            ("drain",),
        ]
        self.assertEqual(replay(script), self.EXPECTED)

    def test_no_resend_after_restore(self):
        # 已消费的变化在恢复后不得重发
        d = Debouncer(3)
        d.ingest("s", "A", 0)
        d.advance(3)
        self.assertEqual(d.drain(), [Change("s", "A", 3)])
        d = Debouncer.restore(json.loads(json.dumps(d.save())))
        self.assertEqual(d.drain(), [])
        d.advance(100)
        self.assertEqual(d.drain(), [])

    def test_unconsumed_confirm_delivered_exactly_once_across_restore(self):
        d = Debouncer(3)
        d.ingest("s", "A", 0)
        d.advance(3)                   # 确认已生成，未消费
        d = Debouncer.restore(json.loads(json.dumps(d.save())))
        self.assertEqual(d.drain(), [Change("s", "A", 3)])  # 恢复后恰好一次
        self.assertEqual(d.drain(), [])

    def test_unfinished_candidate_survives_restore(self):
        # 尚未满足时长的候选（含起始时间）不得丢失
        d = Debouncer(3)
        d.ingest("s", "A", 0)
        d.ingest("s", "B", 1)
        d.advance(2.5)                 # B 稳定了 1.5，未满
        d = Debouncer.restore(json.loads(json.dumps(d.save())))
        d.advance(3.999)
        self.assertEqual(d.drain(), [])
        d.advance(4)                   # 从 1 起算满 3，正常确认
        self.assertEqual(d.drain(), [Change("s", "B", 4)])

    def test_checkpoint_is_equivalent_to_uninterrupted_run(self):
        # 在每条指令之间都插入 checkpoint，结果应与无 checkpoint 完全一致
        plain = [
            ("ingest", "s1", "A", 0),
            ("ingest", "s2", "X", 0),
            ("advance", 2),
            ("ingest", "s1", "B", 2),
            ("ingest", "s2", None, 2.5),
            ("ingest", "s2", "Y", 3),
            ("advance", 5),
            ("drain",),
            ("advance", 6),
            ("drain",),
        ]
        hardened = []
        for op in plain:
            hardened.append(op)
            hardened.append(("checkpoint",))
        self.assertEqual(replay(hardened), replay(plain))


if __name__ == "__main__":
    unittest.main()
