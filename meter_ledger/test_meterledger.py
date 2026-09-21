"""meterledger 测试:场景验证、独立全量重算、崩溃恢复。

运行: python3 -m unittest test_meterledger -v
"""
import json
import os
import shutil
import sys
import tempfile
import unittest
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from meterledger import (Consumer, Journal, MeterLedger, load_consumer,
                         write_checkpoint)


def naive_integrate(mode, samples, gaps, cals, a, b):
    """独立实现的全量重算,用于交叉验证账本的增量结果。"""
    a, b = Fraction(a), Fraction(b)
    samples = sorted(samples)
    points = {a, b}
    for t, _ in samples:
        if a < t < b:
            points.add(t)
    for ga, gb in gaps:
        if a < ga < b:
            points.add(ga)
        if a < gb < b:
            points.add(gb)
    for eff, _, _ in cals:
        if a < eff < b:
            points.add(eff)
    points = sorted(points)

    def raw(t):
        if not samples or t < samples[0][0]:
            return None
        if mode == "step":
            cand = [s for s in samples if s[0] <= t]
            return cand[-1][1]
        if t >= samples[-1][0]:
            return samples[-1][1]
        for (t0, v0), (t1, v1) in zip(samples, samples[1:]):
            if t0 <= t < t1:
                return v0 + (v1 - v0) * (t - t0) / (t1 - t0)
        raise AssertionError("unreachable")

    def cal(t):
        best = None
        for eff, scale, offset in cals:
            if eff <= t and (best is None or eff > best[0]):
                best = (eff, scale, offset)
        return (best[1], best[2]) if best else (Fraction(1), Fraction(0))

    value = Fraction(0)
    missing = Fraction(0)
    for s, e in zip(points, points[1:]):
        mid = (s + e) / 2
        if raw(mid) is None or any(ga <= mid < gb for ga, gb in gaps):
            missing += e - s
            continue
        scale, offset = cal(mid)
        if mode == "step":
            seg = raw(mid) * (e - s)
        else:
            seg = (raw(s) + raw(e)) * (e - s) / 2
        value += scale * seg + offset * (e - s)
    return value, missing


class LedgerCase(unittest.TestCase):
    mode = "step"

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp)
        self.journal = Journal(os.path.join(self.tmp, "journal.log"))
        self.ledger = MeterLedger(self.journal, mode=self.mode)

    def assert_full_recompute(self):
        curve = self.ledger.curve
        samples = [(s.t, s.v) for s in curve.samples]
        gaps = [(g.a, g.b) for g in curve.gaps]
        cals = [(c.effective_from, c.scale, c.offset) for c in curve.calibrations]
        for qid, q in self.ledger.queries.items():
            expected = naive_integrate(self.mode, samples, gaps, cals, q.a, q.b)
            self.assertEqual((q.value, q.missing), expected,
                             f"查询 {qid} 与独立全量重算不一致")


class StepVerificationTest(LedgerCase):
    def test_verification_numbers(self):
        self.ledger.add_sample(0, 2)
        self.ledger.add_sample(10, 5)
        self.ledger.register_query("A", 0, 12)
        self.assertEqual(self.ledger.queries["A"].value, Fraction(30))
        self.assertEqual(self.ledger.queries["A"].missing, Fraction(0))
        self.ledger.add_sample(4, 3)
        self.assertEqual(self.ledger.queries["A"].value, Fraction(36))
        self.assert_full_recompute()

    def test_same_time_correction(self):
        self.ledger.add_sample(0, 2)
        self.ledger.add_sample(4, 3)
        self.ledger.add_sample(10, 5)
        self.ledger.register_query("A", 0, 12)
        self.ledger.add_sample(4, 9)  # 同一时刻改值
        self.assertEqual(self.ledger.queries["A"].value, Fraction(72))
        self.assert_full_recompute()


class ScenarioListingTest(LedgerCase):
    def setUp(self):
        super().setUp()
        self.ledger.add_sample(0, 2)
        self.ledger.add_sample(10, 5)
        self.ledger.register_query("A", 0, 12)
        self.ledger.add_sample(4, 3)
        self.ledger.register_query("B", 3, 9)
        self.ledger.declare_gap(6, 8)
        self.ledger.add_calibration(5, scale=2)

    def test_final_values_and_versions(self):
        a, b = self.ledger.queries["A"], self.ledger.queries["B"]
        self.assertEqual((a.value, a.missing), (Fraction(49), Fraction(2)))
        self.assertEqual((b.value, b.missing), (Fraction(17), Fraction(2)))
        self.assertEqual((a.sample_version, a.calibration_version), (3, 1))
        self.assertEqual((b.sample_version, b.calibration_version), (3, 1))
        self.assert_full_recompute()

    def test_revoke_replace_sequence(self):
        seq = [(d["query_id"], d["kind"], d["result_id"], Fraction(d["value"]),
                Fraction(d["missing"]), d["sample_version"],
                d["calibration_version"]) for d in self.ledger.deltas]
        F = Fraction
        expected = [
            ("A", "publish", "A:r1", F(30), F(0), 2, 0),
            ("A", "revoke", "A:r1", F(30), F(0), 2, 0),
            ("A", "publish", "A:r2", F(36), F(0), 3, 0),
            ("B", "publish", "B:r1", F(17), F(0), 3, 0),
            ("A", "revoke", "A:r2", F(36), F(0), 3, 0),
            ("A", "publish", "A:r3", F(30), F(2), 3, 0),
            ("B", "revoke", "B:r1", F(17), F(0), 3, 0),
            ("B", "publish", "B:r2", F(11), F(2), 3, 0),
            ("A", "revoke", "A:r3", F(30), F(2), 3, 0),
            ("A", "publish", "A:r4", F(49), F(2), 3, 1),
            ("B", "revoke", "B:r2", F(11), F(2), 3, 0),
            ("B", "publish", "B:r3", F(17), F(2), 3, 1),
        ]
        self.assertEqual(seq, expected)


class LinearModeTest(LedgerCase):
    mode = "linear"

    def test_trapezoid_and_tail(self):
        self.ledger.add_sample(0, 0)
        self.ledger.add_sample(10, 10)
        self.ledger.register_query("Q", 0, 10)
        self.assertEqual(self.ledger.queries["Q"].value, Fraction(50))
        self.ledger.add_sample(5, 20)
        self.assertEqual(self.ledger.queries["Q"].value, Fraction(125))
        self.ledger.register_query("T", 0, 12)  # 尾部按最后值外推
        self.assertEqual(self.ledger.queries["T"].value, Fraction(145))
        self.assert_full_recompute()

    def test_unknown_before_first_sample(self):
        self.ledger.add_sample(5, 3)
        self.ledger.register_query("Q", 0, 8)
        q = self.ledger.queries["Q"]
        self.assertEqual(q.missing, Fraction(5))
        self.assertEqual(q.value, Fraction(9))


class GapTest(LedgerCase):
    def test_gap_is_missing_not_zero(self):
        self.ledger.add_sample(0, 2)
        self.ledger.add_sample(10, 5)
        self.ledger.register_query("A", 0, 12)
        self.ledger.declare_gap(6, 8)
        q = self.ledger.queries["A"]
        self.assertEqual(q.missing, Fraction(2))
        self.assertEqual(q.value, Fraction(26))
        self.assertFalse(q.current_result().complete)

    def test_rational_gap(self):
        self.ledger.add_sample(0, Fraction(1, 2))
        self.ledger.register_query("A", 0, 3)
        self.ledger.declare_gap(Fraction(1, 2), Fraction(3, 2))
        q = self.ledger.queries["A"]
        self.assertEqual(q.missing, Fraction(1))
        self.assertEqual(q.value, Fraction(1))
        self.assert_full_recompute()


class CalibrationScopeTest(LedgerCase):
    def test_only_intersecting_queries_updated(self):
        self.ledger.add_sample(0, 2)
        self.ledger.add_sample(10, 5)
        self.ledger.register_query("C", 0, 5)
        self.ledger.register_query("D", 5, 12)
        before = len(self.ledger.deltas)
        self.ledger.add_calibration(5, scale=10)
        new = self.ledger.deltas[before:]
        self.assertEqual({d["query_id"] for d in new}, {"D"})
        self.assertEqual(self.ledger.queries["C"].value, Fraction(10))
        self.assertEqual(self.ledger.queries["D"].value, Fraction(200))
        self.assert_full_recompute()


class FullRecomputeTest(LedgerCase):
    """每步操作后都与独立全量重算对比(含有理数与迟到数据)。"""

    def test_step_scenario(self):
        ops = [
            lambda: self.ledger.add_sample(0, 2),
            lambda: self.ledger.add_sample(10, 5),
            lambda: self.ledger.register_query("A", 0, 12),
            lambda: self.ledger.add_sample(4, 3),
            lambda: self.ledger.register_query("B", 3, 9),
            lambda: self.ledger.declare_gap(6, 8),
            lambda: self.ledger.add_calibration(5, scale=2),
            lambda: self.ledger.add_sample(Fraction(15, 2), Fraction(7, 3)),
            lambda: self.ledger.add_calibration(8, offset=1),
            lambda: self.ledger.declare_gap(Fraction(1, 2), Fraction(3, 2)),
            lambda: self.ledger.register_query("C", Fraction(11, 2), 20),
        ]
        for op in ops:
            op()
            self.assert_full_recompute()


class LinearFullRecomputeTest(LedgerCase):
    mode = "linear"

    def test_linear_scenario(self):
        ops = [
            lambda: self.ledger.add_sample(0, Fraction(1, 2)),
            lambda: self.ledger.add_sample(3, 2),
            lambda: self.ledger.register_query("A", 0, 6),
            lambda: self.ledger.add_sample(Fraction(9, 2), Fraction(7, 3)),
            lambda: self.ledger.declare_gap(1, 2),
            lambda: self.ledger.add_calibration(Fraction(5, 2), scale=Fraction(3, 2)),
            lambda: self.ledger.register_query("B", 2, 9),
            lambda: self.ledger.add_sample(6, 0),
        ]
        for op in ops:
            op()
            self.assert_full_recompute()


class CrashyLedger(MeterLedger):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.armed = False

    def _emit_delta(self, q, kind, result):
        event = super()._emit_delta(q, kind, result)
        if self.armed and kind == "revoke":
            raise RuntimeError("simulated crash")
        return event


class RecoveryTest(LedgerCase):
    def test_crash_between_revoke_and_publish(self):
        journal = self.journal
        ledger = CrashyLedger(journal, mode="step")
        self.ledger = ledger
        ledger.add_sample(0, 2)
        ledger.add_sample(10, 5)
        ledger.register_query("A", 0, 12)
        ledger.add_sample(4, 3)
        ledger.register_query("B", 3, 9)
        ledger.declare_gap(6, 8)

        consumer = Consumer()
        consumer.replay(journal)
        consumer.confirm()
        ckpt = os.path.join(self.tmp, "checkpoint.json")
        write_checkpoint(ckpt, ledger, consumer)

        ledger.armed = True
        with self.assertRaises(RuntimeError):
            ledger.add_calibration(5, scale=2)
        crash_pos = journal.next_pos - 1

        # 生产者恢复:重放日志,补发未完成的修正(稳定 ID)
        recovered = MeterLedger.recover(Journal(journal.path), mode="step")
        emitted = [d["delta_id"] for d in recovered.deltas]
        self.assertEqual(emitted, ["A:r3:revoke", "A:r4:publish",
                                   "B:r2:revoke", "B:r3:publish"])

        # 消费者从检查点重放到崩溃点,再重启继续:重复撤销被去重
        c2 = load_consumer(ckpt)
        c2.replay(journal, until=crash_pos)
        write_checkpoint(ckpt, recovered, c2)
        c3 = load_consumer(ckpt)
        c3.replay(journal)
        c3.confirm()

        self.assertEqual(c3.duplicates, 1)  # 重复撤销被去重,未重复扣减
        self.assertEqual(c3.totals["A"]["value"], Fraction(49))
        self.assertEqual(c3.totals["A"]["missing"], Fraction(2))
        self.assertEqual(c3.totals["B"]["value"], Fraction(17))
        self.assertEqual(c3.totals["B"]["missing"], Fraction(2))
        self.assertEqual(c3.confirmed_position, recovered.journal.next_pos - 1)

    def test_clean_recovery_republishes_nothing(self):
        self.ledger.add_sample(0, 2)
        self.ledger.register_query("A", 0, 10)
        recovered = MeterLedger.recover(Journal(self.journal.path), mode="step")
        self.assertEqual(recovered.deltas, [])
        self.assertEqual(recovered.queries["A"].value, Fraction(20))


class CheckpointTest(LedgerCase):
    def test_checkpoint_contents(self):
        self.ledger.add_sample(0, 2)
        self.ledger.add_sample(10, 5)
        self.ledger.register_query("A", 0, 12)
        consumer = Consumer()
        consumer.replay(self.journal)
        consumer.confirm()
        ckpt = os.path.join(self.tmp, "checkpoint.json")
        write_checkpoint(ckpt, self.ledger, consumer)
        with open(ckpt, encoding="utf-8") as f:
            data = json.load(f)
        # 未闭合采样段
        self.assertEqual(data["unclosed_segment"],
                         {"start": "10", "value": "5", "mode": "step"})
        # 查询索引
        self.assertEqual(data["query_index"]["A"]["result_id"], "A:r1")
        self.assertEqual(data["query_index"]["A"]["value"], "30")
        self.assertEqual(data["query_index"]["A"]["sample_version"], 2)
        # 去重状态与确认位置
        self.assertEqual(data["dedup"]["last_position"], consumer.position)
        self.assertIn("A:r1:publish", data["dedup"]["applied_delta_ids"])
        self.assertEqual(data["confirmed_position"], consumer.confirmed_position)


if __name__ == "__main__":
    unittest.main()
