"""压缩器:静态曲线、多通道联动且音频独立、阈值热更新平滑、增益速率受限。"""
import math
import unittest

import fixtures
from dsp import BandCompressor, Block, static_gain_db
from tests.util import goertzel

FS = fixtures.SAMPLE_RATE


class TestStaticCurve(unittest.TestCase):
    def test_below_threshold_zero_gain(self):
        self.assertEqual(static_gain_db(-30.0, -20.0, 4.0, 0.0), 0.0)
        self.assertEqual(static_gain_db(-30.0, -20.0, 4.0, 6.0), 0.0)

    def test_above_threshold_hard_knee(self):
        # 超阈 20 dB,4:1 → 增益 -(20)*(1-1/4) = -15 dB
        self.assertAlmostEqual(static_gain_db(0.0, -20.0, 4.0, 0.0), -15.0)

    def test_soft_knee_continuity(self):
        thr, ratio, knee = -20.0, 4.0, 8.0
        lo, hi = thr - knee / 2, thr + knee / 2
        self.assertAlmostEqual(static_gain_db(lo, thr, ratio, knee), 0.0)
        at_hi = static_gain_db(hi, thr, ratio, knee)
        above = (thr - hi) * (1.0 - 1.0 / ratio)
        self.assertAlmostEqual(at_hi, above, places=12)
        mid = static_gain_db(thr, thr, ratio, knee)
        self.assertAlmostEqual(mid, (1.0 / ratio - 1.0) * knee / 8.0,
                               places=12)

    def test_ratio_one_is_transparent(self):
        self.assertEqual(static_gain_db(10.0, -20.0, 1.0, 6.0), 0.0)
        self.assertEqual(static_gain_db(10.0, -20.0, 1.0, 0.0), 0.0)


class TestLinkedGainIndependentAudio(unittest.TestCase):
    def test_shared_gain_independent_audio(self):
        n = 6000
        left = [0.5] * n
        right = [0.05] * n
        comp = BandCompressor(FS, 2, threshold_db=-20.0, ratio=4.0,
                              knee_db=0.0, attack_s=0.001, release_s=0.05)
        out, tel = comp.process(Block([left, right]))
        # 稳态增益由左声道(较响)决定,右声道联动
        g_expected = static_gain_db(20.0 * math.log10(0.5), -20.0, 4.0, 0.0)
        self.assertAlmostEqual(tel.gain_db[-1], g_expected, places=4)
        lin = 10.0 ** (tel.gain_db[-1] / 20.0)
        self.assertAlmostEqual(out.channels[0][-1], 0.5 * lin, places=9)
        self.assertAlmostEqual(out.channels[1][-1], 0.05 * lin, places=9)
        # 右声道自身包络远低于阈值,仍被联动衰减
        self.assertLess(out.channels[1][-1], 0.05 * 0.5)

    def test_audio_content_stays_independent(self):
        # N=4800 → 10 Hz 分辨率,1 kHz/4 kHz 均为精确 bin,无泄漏
        n = 9600
        left = fixtures.sine(n, 1000.0, 0.5, channels=1)[0]
        right = fixtures.sine(n, 4000.0, 0.05, channels=1)[0]
        comp = BandCompressor(FS, 2, threshold_db=-20.0, ratio=4.0,
                              knee_db=0.0, attack_s=0.001, release_s=0.05)
        out, tel = comp.process(Block([left, right]))
        g = 10.0 ** (tel.gain_db[-1] / 20.0)
        # 右声道输出仍是 4 kHz 音(幅度按比例缩放),无左声道 1 kHz 串扰
        _, mag4k = goertzel(out.channels[1], 4800, 4800, 4000.0, FS)
        _, mag1k = goertzel(out.channels[1], 4800, 4800, 1000.0, FS)
        self.assertAlmostEqual(mag4k, 0.05 * g, delta=1e-4)
        self.assertLess(mag1k, 1e-6)


class TestThresholdHotUpdate(unittest.TestCase):
    def test_threshold_ramps_without_steps(self):
        n1, n2 = 3000, 6000
        slew = 480.0  # dB/s → 0.01 dB/样本
        comp = BandCompressor(FS, 2, threshold_db=-10.0, ratio=4.0,
                              knee_db=0.0, attack_s=0.001, release_s=0.05,
                              threshold_slew_db_per_s=slew)
        data = fixtures.constant(n1 + n2, [0.5, 0.5])
        block = Block(data)
        _, tel1 = comp.process(block.slice(0, n1))
        comp.set_threshold_db(-30.0)
        out2, tel2 = comp.process(block.slice(n1, n1 + n2))
        # 阈值轨迹:每步恰为声明斜率,无跳变,最终到达目标
        step = slew / FS
        for i in range(1, len(tel2.threshold_db)):
            d = tel2.threshold_db[i] - tel2.threshold_db[i - 1]
            self.assertGreaterEqual(d, -step - fixtures.RATE_TOLERANCE_DB)
            self.assertLessEqual(d, 0.0)
        self.assertEqual(tel2.threshold_db[0],
                         tel1.threshold_db[-1] - step)
        self.assertEqual(tel2.threshold_db[-1], -30.0)
        # 增益轨迹同样平滑(受增益速率限制)
        atk = 2000.0 / FS
        for i in range(1, len(tel2.gain_db)):
            d = tel2.gain_db[i] - tel2.gain_db[i - 1]
            self.assertGreaterEqual(d, -atk - fixtures.RATE_TOLERANCE_DB)
        # 最终增益符合新阈值的静态曲线
        g_final = static_gain_db(20.0 * math.log10(0.5), -30.0, 4.0, 0.0)
        self.assertAlmostEqual(tel2.gain_db[-1], g_final, places=4)
        self.assertAlmostEqual(out2.channels[0][-1],
                               0.5 * 10.0 ** (g_final / 20.0), places=9)


class TestGainRateLimited(unittest.TestCase):
    def test_attack_rate_bound(self):
        n = 6000
        data = [[0.1] * 2000 + [1.0] * (n - 2000)] * 2
        atk_slew = 2000.0
        comp = BandCompressor(FS, 2, threshold_db=-20.0, ratio=4.0,
                              knee_db=0.0, attack_s=1e-5, release_s=0.05,
                              gain_attack_db_per_s=atk_slew)
        _, tel = comp.process(Block(data))
        step = atk_slew / FS
        worst = 0.0
        for i in range(1, n):
            d = tel.gain_db[i] - tel.gain_db[i - 1]
            self.assertGreaterEqual(
                d, -step - fixtures.RATE_TOLERANCE_DB)
            worst = min(worst, d)
        # 电平突变后增益确实以声明速率压下去
        self.assertAlmostEqual(worst, -step, delta=1e-9)


if __name__ == "__main__":
    unittest.main()
