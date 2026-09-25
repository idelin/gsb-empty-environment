"""前瞻限幅器:峰值限制、块边界、阈值热更新、取消/恢复、联动、速率、无裁平。"""
import math
import random
import unittest

import fixtures
from dsp import Block, LookaheadLimiter
from tests.util import argmax_abs, has_flat_run

FS = fixtures.SAMPLE_RATE
L = fixtures.LIMITER_LOOKAHEAD


def make_limiter(**over):
    params = fixtures.limiter_params()
    params.update(over)
    return LookaheadLimiter(FS, 2, **params)


def process_in_blocks(lim, block, sizes):
    """按给定块大小序列处理,返回拼接后的 (channels, valid, gain_db, thr_db)。"""
    outs = [[] for _ in range(block.n_channels)]
    gains = []
    thrs = []
    pos = 0
    for s in sizes:
        o, tel = lim.process(block.slice(pos, min(pos + s, block.n_samples)))
        for c in range(block.n_channels):
            outs[c].extend(o.channels[c])
        gains.extend(tel.gain_db)
        thrs.extend(tel.threshold_db)
        pos += s
    return outs, gains, thrs


class TestImpulseLimiting(unittest.TestCase):
    def test_impulse_limited_to_threshold(self):
        n = 2048
        t0 = 500
        lim = make_limiter(threshold_db=-6.0)
        data = fixtures.impulse(n, t0, amp=2.0)
        out, tel = lim.process(Block(data))
        thr = 10.0 ** (-6.0 / 20.0)
        peak = max(abs(x) for x in out.channels[0])
        self.assertLessEqual(peak, thr + fixtures.OVERSHOOT_TOLERANCE)
        self.assertGreater(peak, thr * 0.95)  # 恰好压到阈值附近,未过度压制
        self.assertEqual(argmax_abs(out.channels[0]), t0 + L)
        self.assertEqual(tel.latency_samples, L)
        self.assertAlmostEqual(min(tel.gain_db),
                               -6.0 - 20.0 * math.log10(2.0), places=6)
        # 超限证据:输入峰值、施加增益、输出峰值俱全
        self.assertEqual(len(lim.events), 1)
        ev = lim.events[0]
        self.assertAlmostEqual(ev["input_peak_db"], 20.0 * math.log10(2.0),
                               places=6)
        self.assertAlmostEqual(ev["output_peak_db"], -6.0, places=3)
        self.assertLessEqual(ev["output_peak_db"],
                             ev["threshold_db"] + 0.01)

    def test_peak_at_block_boundary(self):
        n = 1024
        t0 = 255  # 恰好位于 256 样本块的交界
        data = fixtures.impulse(n, t0, amp=2.0)
        lim_full = make_limiter(threshold_db=-6.0)
        out_full, _ = lim_full.process(Block(data))
        lim_blk = make_limiter(threshold_db=-6.0)
        outs, gains, _ = process_in_blocks(
            lim_blk, Block(data), [256, 256, 256, 256])
        for c in range(2):
            self.assertEqual(outs[c], out_full.channels[c])
        thr = 10.0 ** (-6.0 / 20.0)
        self.assertLessEqual(max(abs(x) for x in outs[0]),
                             thr + fixtures.OVERSHOOT_TOLERANCE)
        self.assertEqual(argmax_abs(outs[0]), t0 + L)


class TestThresholdHotUpdate(unittest.TestCase):
    def test_threshold_change_mid_lookahead(self):
        n = 2048
        t0 = 1000
        t_change = t0 + L // 2  # 峰值已进入前瞻窗、尚未离开
        lim = make_limiter(threshold_db=0.0,
                           threshold_slew_db_per_s=48000.0)  # 1 dB/样本
        data = fixtures.impulse(n, t0, amp=4.0)
        block = Block(data)
        out1, tel1 = lim.process(block.slice(0, t_change))
        lim.set_threshold_db(-12.0)
        out2, tel2 = lim.process(block.slice(t_change, n))
        # 不变式:任意时刻输出不超过当时的平滑阈值
        for c in range(2):
            for i, x in enumerate(out1.channels[c]):
                self.assertLessEqual(abs(x),
                                     10.0 ** (tel1.threshold_db[i] / 20.0)
                                     + fixtures.OVERSHOOT_TOLERANCE)
            for i, x in enumerate(out2.channels[c]):
                self.assertLessEqual(abs(x),
                                     10.0 ** (tel2.threshold_db[i] / 20.0)
                                     + fixtures.OVERSHOOT_TOLERANCE)
        # 阈值斜率平滑:轨迹无跳变,逐步逼近 -12 dB
        self.assertEqual(tel2.threshold_db[0],
                         tel1.threshold_db[-1] - 1.0)
        self.assertEqual(tel2.threshold_db[-1], -12.0)
        # 峰值最终被限到新阈值
        peak = max(abs(x) for x in out2.channels[0])
        self.assertAlmostEqual(peak, 10.0 ** (-12.0 / 20.0), delta=1e-3)

    def test_random_threshold_sweeps_invariant(self):
        rng = random.Random(0)
        n = 4000
        data = [[rng.uniform(-1.0, 1.0) * (3.0 if rng.random() < 0.05 else 0.3)
                 for _ in range(n)] for _ in range(2)]
        lim = make_limiter(threshold_db=-3.0,
                           threshold_slew_db_per_s=4800.0)
        block = Block(data)
        outs = [[] for _ in range(2)]
        thrs = []
        pos = 0
        while pos < n:
            end = min(n, pos + 256)
            o, tel = lim.process(block.slice(pos, end))
            for c in range(2):
                outs[c].extend(o.channels[c])
            thrs.extend(tel.threshold_db)
            if rng.random() < 0.3:
                lim.set_threshold_db(rng.uniform(-12.0, 0.0))
            pos = end
        for c in range(2):
            for t, x in enumerate(outs[c]):
                self.assertLessEqual(
                    abs(x),
                    10.0 ** (thrs[t] / 20.0) * (1.0 + 1e-9) + 1e-12)


class TestCancelReengage(unittest.TestCase):
    def test_cancel_and_reengage(self):
        n = 4096
        data = fixtures.two_bursts(n, 1000.0, 2.0, [(400, 600), (3000, 3200)])
        thr = 10.0 ** (-6.0 / 20.0)
        lim = make_limiter(threshold_db=-6.0)
        block = Block(data)
        o1, _ = lim.process(block.slice(0, 1000))       # 突发1:限幅
        lim.set_bypass(True)
        o2, _ = lim.process(block.slice(1000, 2400))    # 取消:直通
        lim.set_bypass(False)
        o3, _ = lim.process(block.slice(2400, n))       # 恢复:突发2仍限幅
        merged = [o1.channels[c] + o2.channels[c] + o3.channels[c]
                  for c in range(2)]
        for c in range(2):
            burst1 = max(abs(x) for x in merged[c][400 + L:600 + L])
            burst2 = max(abs(x) for x in merged[c][3000 + L:3200 + L])
            self.assertLessEqual(burst1, thr + fixtures.OVERSHOOT_TOLERANCE)
            self.assertLessEqual(burst2, thr + fixtures.OVERSHOOT_TOLERANCE)
        # 取消期间无双延迟:输出恰为输入延迟 L 样本(逐位相等)
        for c in range(2):
            self.assertEqual(merged[c][1000:2400], data[c][1000 - L:2400 - L])

    def test_bypass_does_not_change_delay(self):
        n = 2048
        data = fixtures.sine(n, 1000.0, 0.05)  # 低于阈值,增益恒 0 dB
        lim_on = make_limiter(threshold_db=-6.0)
        lim_off = make_limiter(threshold_db=-6.0, bypass=True)
        out_on, _ = lim_on.process(Block(data))
        out_off, _ = lim_off.process(Block(data))
        for c in range(2):
            self.assertEqual(out_on.channels[c], out_off.channels[c])
            self.assertEqual(out_on.channels[c][L:], data[c][:n - L])


class TestLinkedChannels(unittest.TestCase):
    def test_right_only_spike_links_both_channels(self):
        n = 2048
        t0 = 800
        left = fixtures.sine(n, 1000.0, 0.05, channels=1)[0]
        right = fixtures.impulse(n, t0, amp=3.0, channels=1)[0]
        lim = make_limiter(threshold_db=-6.0)
        out, tel = lim.process(Block([left, right]))
        gmin = min(tel.gain_db)
        self.assertAlmostEqual(gmin, -6.0 - 20.0 * math.log10(3.0), places=6)
        g = 10.0 ** (gmin / 20.0)
        # 左声道自身无尖峰,却在同一时刻被联动衰减
        sample = out.channels[0][t0 + L]
        self.assertAlmostEqual(sample, left[t0] * g, delta=1e-12)
        self.assertLess(abs(sample), 0.05 * 0.5)
        # 右声道尖峰被限到阈值
        self.assertLessEqual(max(abs(x) for x in out.channels[1]),
                             10.0 ** (-6.0 / 20.0)
                             + fixtures.OVERSHOOT_TOLERANCE)
        # 两通道音频保持独立:左声道仍是连续正弦而非脉冲
        self.assertNotEqual(argmax_abs(out.channels[0]),
                            argmax_abs(out.channels[1]))


class TestGainRateAndWaveform(unittest.TestCase):
    def test_release_rate_declared(self):
        n = 4096
        release = 120.0
        lim = make_limiter(threshold_db=-6.0, release_db_per_s=release)
        data = fixtures.impulse(n, 500, amp=4.0)
        _, tel = lim.process(Block(data))
        step = release / FS
        for i in range(1, n):
            rise = tel.gain_db[i] - tel.gain_db[i - 1]
            self.assertLessEqual(rise, step + fixtures.RATE_TOLERANCE_DB)

    def test_no_hard_clipping(self):
        n = 8192
        lim = make_limiter(threshold_db=-6.0)
        data = fixtures.sine(n, 1000.0, 2.5)
        out, tel = lim.process(Block(data))
        thr = 10.0 ** (-6.0 / 20.0)
        self.assertLessEqual(max(abs(x) for x in out.channels[0]),
                             thr + fixtures.OVERSHOOT_TOLERANCE)
        # 硬裁平会产生连续等值平台;平滑增益不会(跳过启动的延迟线前缀零)
        self.assertFalse(has_flat_run(out.channels[0][L:]))
        # 超限证据非空(持续压限为进行中事件)且输出不超阈值
        self.assertTrue(len(tel.events) >= 1)
        for ev in tel.events:
            self.assertLessEqual(ev["output_peak_db"],
                                 ev["threshold_db"] + 0.01)
            self.assertGreater(ev["would_exceed_db"], 0.0)


if __name__ == "__main__":
    unittest.main()
