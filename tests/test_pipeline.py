"""端到端:声明延迟与实测一致、分块不变性、遥测完整、旁路全链路重建。"""
import unittest

import fixtures
from dsp import Block, Pipeline
from tests.util import argmax_abs, max_abs_err


def make_pipeline(bypass=False):
    params = (fixtures.bypass_band_params() if bypass
              else fixtures.band_compressor_params())
    return Pipeline(fixtures.SAMPLE_RATE, 2,
                    fixtures.crossover_coefficients(), params,
                    fixtures.limiter_params())


class TestDeclaredLatency(unittest.TestCase):
    def test_measured_delay_matches_declared(self):
        n = 4096
        t0 = 1000
        # 微小脉冲:不触发压缩与限幅,纯测延迟
        data = fixtures.impulse(n, t0, amp=1e-6)
        pipe = make_pipeline()
        out, tel = pipe.process(Block(data))
        report = tel["latency"]
        self.assertEqual(report["stages"],
                         {"crossover": fixtures.CROSSOVER_LATENCY,
                          "limiter": fixtures.LIMITER_LOOKAHEAD})
        self.assertEqual(report["total"], fixtures.TOTAL_LATENCY)
        self.assertEqual(argmax_abs(out.channels[0]),
                         t0 + report["total"])

    def test_bypass_pipeline_reconstructs_reference(self):
        n = 4096
        tones = [(150.0, 0.2), (900.0, 0.15), (2500.0, 0.1), (6000.0, 0.1)]
        data = fixtures.multitone(n, tones)
        pipe = make_pipeline(bypass=True)
        pipe.limiter.set_bypass(True)
        out, tel = pipe.process(Block(data))
        total = tel["latency"]["total"]
        for c in range(2):
            err = max_abs_err(out.channels[c][total:], data[c][:n - total])
            self.assertLess(err, fixtures.PR_TOLERANCE)


class TestBlockInvariance(unittest.TestCase):
    def test_arbitrary_block_sizes_identical(self):
        n = 3000
        tones = [(200.0, 0.3), (1500.0, 0.4)]
        data = fixtures.multitone(n, tones)
        for c in range(2):  # 叠加一个超阈尖峰,让限幅器也参与
            data[c][1777] = 2.0
        block = Block(data)
        reference = None
        for sizes in ([n], [256] * 11 + [184], [777, 1, 2222]):
            pipe = make_pipeline()
            chunks = [[] for _ in range(2)]
            pos = 0
            for s in sizes:
                o, _ = pipe.process(block.slice(pos, min(pos + s, n)))
                for c in range(2):
                    chunks[c].extend(o.channels[c])
                pos += s
            if reference is None:
                reference = chunks
            else:
                for c in range(2):
                    self.assertEqual(chunks[c], reference[c])


class TestTelemetry(unittest.TestCase):
    def test_per_stage_latency_gain_and_evidence(self):
        n = 8192
        data = fixtures.sine(n, 500.0, 1.5)  # 超阈,压缩与限幅均动作
        pipe = make_pipeline()
        out, tel = pipe.process(Block(data))
        # 逐阶段延迟
        self.assertEqual(tel["crossover"]["latency_samples"],
                         fixtures.CROSSOVER_LATENCY)
        self.assertEqual(tel["limiter"].latency_samples,
                         fixtures.LIMITER_LOOKAHEAD)
        # 分带压缩增益轨迹(3 带,长度 = 样本数,确有压限)
        self.assertEqual(len(tel["crossover"]["bands"]), 3)
        for band in tel["crossover"]["bands"]:
            self.assertEqual(len(band.gain_db), n)
        self.assertLess(min(tel["crossover"]["bands"][0].gain_db), 0.0)
        # 限幅器增益轨迹与超限证据
        self.assertEqual(len(tel["limiter"].gain_db), n)
        self.assertLess(min(tel["limiter"].gain_db), 0.0)
        self.assertTrue(len(tel["limiter"].events) >= 1)
        ev = tel["limiter"].events[0]
        for key in ("start", "end", "input_peak_db", "threshold_db",
                    "min_gain_db", "output_peak_db", "would_exceed_db"):
            self.assertIn(key, ev)
        self.assertLessEqual(ev["output_peak_db"],
                             ev["threshold_db"] + 0.01)
        # 输出未被裁平:峰值恰在阈值附近
        peak = max(abs(x) for x in out.channels[0])
        thr = 10.0 ** (fixtures.limiter_params()["threshold_db"] / 20.0)
        self.assertLessEqual(peak, thr + fixtures.OVERSHOOT_TOLERANCE)
        self.assertGreater(peak, thr * 0.9)


if __name__ == "__main__":
    unittest.main()
