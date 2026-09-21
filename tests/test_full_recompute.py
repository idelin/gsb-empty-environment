"""独立全量重算测试:不依赖 Ledger 的增量状态,从零重算并比对。"""
import os
import shutil
import tempfile
import unittest
from fractions import Fraction

from meter_ledger import LINEAR, Ledger, SampleCurve, SimulatedCrash


def reference_integrate(samples, gaps, calibrations, a, b):
    """独立参考实现:按整数时刻逐单位步进全量重算(仅用于整数场景)。

    samples: [(t, v)] 阶梯采样;gaps: [(s, e)];calibrations: [(eff, k, b)]。
    返回 (已知积分, 缺测长度)。断测区间只计缺测,不计零值。
    """
    total = Fraction(0)
    missing = Fraction(0)
    t = Fraction(a)
    while t < Fraction(b):
        value = None
        for ts, v in samples:
            if Fraction(ts) <= t:
                value = Fraction(v)
        if any(Fraction(s) <= t < Fraction(e) for s, e in gaps) or value is None:
            missing += 1
        else:
            scale, offset = Fraction(1), Fraction(0)
            for eff, k, b0 in calibrations:
                if Fraction(eff) <= t:
                    scale, offset = Fraction(k), Fraction(b0)
            total += scale * value + offset
        t += 1
    return total, missing


class StepScenarioTest(unittest.TestCase):
    """题面场景:逐步验证增量账本与独立全量重算一致。"""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.ledger = Ledger(
            os.path.join(self.tmp, "j.log"), os.path.join(self.tmp, "c.json")
        )

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def current(self, qid):
        r = self.ledger.published[qid]
        return Fraction(r["integral"]), Fraction(r["missing"])

    def test_step_mode_and_corrections(self):
        samples = [(0, 2), (10, 5)]
        gaps = []
        cals = []
        self.ledger.add_sample(0, 2)
        self.ledger.add_sample(10, 5)
        self.ledger.register_query("Q1", 0, 12)
        self.assertEqual(self.current("Q1"), (Fraction(30), Fraction(0)))
        self.assertEqual(
            self.current("Q1"), reference_integrate(samples, gaps, cals, 0, 12)
        )

        samples = [(0, 2), (4, 3), (10, 5)]
        self.ledger.add_sample(4, 3)
        self.assertEqual(self.current("Q1"), (Fraction(36), Fraction(0)))
        self.assertEqual(
            self.current("Q1"), reference_integrate(samples, gaps, cals, 0, 12)
        )

        self.ledger.register_query("Q2", 3, 9)
        self.assertEqual(
            self.current("Q2"), reference_integrate(samples, gaps, cals, 3, 9)
        )

        gaps = [(6, 8)]
        self.ledger.declare_gap(6, 8)
        self.assertEqual(self.current("Q1"), (Fraction(30), Fraction(2)))
        self.assertEqual(self.current("Q2"), (Fraction(11), Fraction(2)))
        self.assertEqual(
            self.current("Q1"), reference_integrate(samples, gaps, cals, 0, 12)
        )
        self.assertEqual(
            self.current("Q2"), reference_integrate(samples, gaps, cals, 3, 9)
        )

        cals = [(5, 2, 0)]
        self.ledger.add_calibration(5, scale=2, offset=0)
        self.assertEqual(self.current("Q1"), (Fraction(49), Fraction(2)))
        self.assertEqual(self.current("Q2"), (Fraction(17), Fraction(2)))
        self.assertEqual(
            self.current("Q1"), reference_integrate(samples, gaps, cals, 0, 12)
        )
        self.assertEqual(
            self.current("Q2"), reference_integrate(samples, gaps, cals, 3, 9)
        )

    def test_versions_recorded(self):
        self.ledger.add_sample(0, 2)
        self.ledger.add_sample(10, 5)
        self.ledger.register_query("Q1", 0, 12)
        self.ledger.add_sample(4, 3)
        self.ledger.declare_gap(6, 8)
        self.ledger.add_calibration(5, scale=2)
        r = self.ledger.published["Q1"]
        self.assertEqual(r["sample_version"], 4)
        self.assertEqual(r["calibration_version"], 1)
        self.assertTrue(r["delta_id"].endswith(":Q1:publish"))
        self.assertGreater(r["position"], 0)


class LinearModeTest(unittest.TestCase):
    def test_linear_with_gap(self):
        curve = SampleCurve(LINEAR)
        curve.add_sample(0, 0)
        curve.add_sample(10, 10)
        known, missing = curve.integrate(0, 10)
        self.assertEqual((known, missing), (Fraction(50), Fraction(0)))
        curve.declare_gap(2, 4)
        known, missing = curve.integrate(0, 10)
        self.assertEqual((known, missing), (Fraction(44), Fraction(2)))

    def test_linear_calibration(self):
        curve = SampleCurve(LINEAR)
        curve.add_sample(0, 0)
        curve.add_sample(10, 10)
        curve.add_calibration(5, scale=2, offset=1)
        # [0,5): ∫t dt = 25/2; [5,10): ∫(2t+1) dt = (100+10)-(25+5) = 80
        known, missing = curve.integrate(0, 10)
        self.assertEqual(known, Fraction(25, 2) + Fraction(80))
        self.assertEqual(missing, Fraction(0))


class RecoveryTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.journal = os.path.join(self.tmp, "j.log")
        self.ckpt = os.path.join(self.tmp, "c.json")

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def _build_history(self):
        ledger = Ledger(self.journal, self.ckpt)
        ledger.add_sample(0, 2)
        ledger.add_sample(10, 5)
        ledger.add_sample(4, 3)
        ledger.register_query("Q1", 0, 12)
        ledger.register_query("Q2", 3, 9)
        ledger.declare_gap(6, 8)
        ledger.checkpoint()
        return ledger

    def test_crash_between_revoke_and_publish(self):
        self._build_history()
        crashed = Ledger(self.journal, self.ckpt)

        def hook(delta_id, kind, qid):
            if kind == "publish":
                raise SimulatedCrash

        crashed.delta_hook = hook
        with self.assertRaises(SimulatedCrash):
            crashed.add_calibration(5, scale=2, offset=0)
        del crashed

        recovered = Ledger(self.journal, self.ckpt)
        self.assertTrue(any("未提交事务" in m for m in recovered.recovery_log))
        self.assertTrue(any("去重跳过" in m for m in recovered.recovery_log))
        self.assertEqual(Fraction(recovered.published["Q1"]["integral"]), Fraction(49))
        self.assertEqual(Fraction(recovered.published["Q2"]["integral"]), Fraction(17))
        self.assertEqual(Fraction(recovered.published["Q1"]["missing"]), Fraction(2))

        # 独立全量重算比对恢复后的状态
        samples = [(0, 2), (4, 3), (10, 5)]
        gaps = [(6, 8)]
        cals = [(5, 2, 0)]
        self.assertEqual(
            (Fraction(recovered.published["Q1"]["integral"]),
             Fraction(recovered.published["Q1"]["missing"])),
            reference_integrate(samples, gaps, cals, 0, 12),
        )
        self.assertEqual(
            (Fraction(recovered.published["Q2"]["integral"]),
             Fraction(recovered.published["Q2"]["missing"])),
            reference_integrate(samples, gaps, cals, 3, 9),
        )

        # 二次重放:不得重复扣减
        recovered.checkpoint()
        replayed = Ledger(self.journal, self.ckpt)
        self.assertEqual(replayed.published, recovered.published)
        self.assertEqual(len(replayed.applied_deltas), len(recovered.applied_deltas))

    def test_replay_idempotent_across_checkpoints(self):
        ledger = self._build_history()
        ledger.add_calibration(5, scale=2, offset=0)
        ledger.checkpoint()
        ledger.add_sample(7, 9)
        ledger.checkpoint()
        snapshot = dict(ledger.published)

        reopened = Ledger(self.journal, self.ckpt)
        self.assertEqual(reopened.published, snapshot)
        self.assertEqual(reopened.curve.sample_version, ledger.curve.sample_version)
        self.assertEqual(
            reopened.curve.calibration_version, ledger.curve.calibration_version
        )
        # 冷启动(无 checkpoint)全量重放,结果一致
        os.remove(self.ckpt)
        cold = Ledger(self.journal, self.ckpt)
        self.assertEqual(cold.published, snapshot)


if __name__ == "__main__":
    unittest.main()
