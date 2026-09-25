"""缺测/未知样本:标记沿各处理级传播,NaN 自动判无效,输出不含 NaN。"""
import math
import unittest

import fixtures
from dsp import Block, DelayLine, FIRFilter, LookaheadLimiter, Pipeline


class TestBlockSanitize(unittest.TestCase):
    def test_nan_marked_invalid_and_zeroed(self):
        b = Block([[float("nan"), 1.0, 2.0]])
        self.assertEqual(b.valid[0], [False, True, True])
        self.assertEqual(b.channels[0][0], 0.0)


class TestStagePropagation(unittest.TestCase):
    def test_fir_taint_covers_tap_span(self):
        fir = FIRFilter([0.2] * 5)  # 群延迟 2
        data = list(range(20))
        valid = [True] * 20
        valid[7] = False
        out, outv = fir.process([float(x) for x in data], valid)
        invalid = [t for t, v in enumerate(outv) if not v]
        self.assertEqual(invalid, [7, 8, 9, 10, 11])
        # 未受污染区域数值正确
        self.assertAlmostEqual(out[6], 0.2 * sum(data[2:7]))

    def test_delay_shifts_flags(self):
        d = DelayLine(3)
        out, outv = d.process([1.0] * 10,
                              [True, True, True, True, False,
                               True, True, True, True, True])
        self.assertEqual([t for t, v in enumerate(outv) if not v], [7])

    def test_limiter_audio_path_and_window_taint(self):
        L = 8
        lim = LookaheadLimiter(fixtures.SAMPLE_RATE, 1,
                               lookahead_samples=L, threshold_db=-1.0)
        n = 64
        data = [[0.1] * n]
        valid = [[True] * n]
        valid[0][20] = False
        out, _ = lim.process(Block(data, valid))
        # 音频路径:t=20+L;检测窗:含该样本的输出 t ∈ [20, 20+L]
        invalid = [t for t, v in enumerate(out.valid[0]) if not v]
        self.assertEqual(invalid, list(range(20, 20 + L + 1)))
        self.assertTrue(all(math.isfinite(x) for x in out.channels[0]))


class TestPipelinePropagation(unittest.TestCase):
    def test_missing_region_propagates_with_latency(self):
        n = 2000
        a, b = 1000, 1010
        data = fixtures.multitone(n, [(440.0, 0.1)])
        block = fixtures.inject_missing(Block(data), [(a, b)])
        pipe = Pipeline(fixtures.SAMPLE_RATE, 2,
                        fixtures.crossover_coefficients(),
                        fixtures.bypass_band_params(),
                        fixtures.limiter_params())
        out, tel = pipe.process(block)
        # 分频:最长带 181 抽头 → 污染扩展至 b-1+180 = 1189
        # 限幅:音频路径 +64,检测窗向后覆盖 64 → 无效区间 [1000, 1253]
        lo = a
        hi = b - 1 + 180 + fixtures.LIMITER_LOOKAHEAD
        for c in range(2):
            invalid = [t for t, v in enumerate(out.valid[c]) if not v]
            self.assertEqual(invalid, list(range(lo, hi + 1)))
            self.assertTrue(all(math.isfinite(x) for x in out.channels[c]))
        # 有效区域数值不受污染:与无缺测参考一致
        ref, _ = pipe.__class__(fixtures.SAMPLE_RATE, 2,
                                fixtures.crossover_coefficients(),
                                fixtures.bypass_band_params(),
                                fixtures.limiter_params()).process(Block(data))
        total = tel["latency"]["total"]
        for t in list(range(total, lo)) + list(range(hi + 1, n)):
            self.assertEqual(out.channels[0][t], ref.channels[0][t])


if __name__ == "__main__":
    unittest.main()
