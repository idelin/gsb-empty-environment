"""核心算法:窗口 RMS、迟滞、最短持续规则、保留量裁剪、延迟声明。"""
import math
import unittest

from silence_split import fixtures
from silence_split.constants import NUMERIC_TOLERANCE
from silence_split.core import Hysteresis, absorb_short_sound, sound_cores, window_rms
from silence_split.segments import build_segments, make_segment_id


class TestWindowRms(unittest.TestCase):
    def test_constant_signal(self):
        self.assertAlmostEqual(window_rms(fixtures.constant(80, 0.3)), 0.3, places=12)

    def test_sine_amplitude(self):
        rms = window_rms(fixtures.tone(80, amp=0.5, freq=400.0))
        self.assertAlmostEqual(rms, 0.5 / math.sqrt(2), delta=1e-6)

    def test_empty(self):
        self.assertEqual(window_rms([]), 0.0)


class TestHysteresis(unittest.TestCase):
    def test_mid_band_holds_state(self):
        h = Hysteresis(0.2, 0.1)
        seq = [0.3, 0.15, 0.15, 0.05, 0.15, 0.3]
        self.assertEqual([h.classify(r) for r in seq],
                         [True, True, True, False, False, True])

    def test_threshold_boundary_uses_tolerance(self):
        # 恰好等于门限:不翻转(容差死区)
        self.assertFalse(Hysteresis(0.2, 0.1).classify(0.2))
        self.assertTrue(Hysteresis(0.2, 0.1, state=True).classify(0.1))
        # 明显越界:翻转
        self.assertTrue(
            Hysteresis(0.2, 0.1).classify(0.2 * (1.0 + 2 * NUMERIC_TOLERANCE)))
        self.assertFalse(
            Hysteresis(0.2, 0.1, state=True).classify(0.1 * (1.0 - 2 * NUMERIC_TOLERANCE)))


class TestMinDurationRules(unittest.TestCase):
    def test_short_silence_merges_long_silence_splits(self):
        flags = [True] * 5 + [False] * 3 + [True] * 5  # 3 < 4 → 合并
        self.assertEqual(sound_cores(flags, 4), [(0, 13)])
        flags = [True] * 5 + [False] * 4 + [True] * 5  # 4 >= 4 → 断开
        self.assertEqual(sound_cores(flags, 4), [(0, 5), (9, 14)])

    def test_short_blip_absorbed_or_breaks(self):
        # 有声4 + 静音2 + 噪点1 + 静音2 + 有声4;min_silence = 4 窗口
        flags = [True] * 4 + [False] * 2 + [True] + [False] * 2 + [True] * 4
        # min_sound = 2 窗口:噪点被吸收 → 静音连续 5 ≥ 4 → 断开为两段
        sm = absorb_short_sound(flags, 2, right_closed=True)
        self.assertEqual(sound_cores(sm, 4), [(0, 4), (9, 13)])
        # min_sound = 1 窗口:噪点有效 → 两段静音各 2 < 4 → 合并为一段
        sm = absorb_short_sound(flags, 1, right_closed=True)
        self.assertEqual(sound_cores(sm, 4), [(0, 13)])

    def test_leading_and_trailing_silence_produce_no_segment(self):
        flags = [False] * 6 + [True] * 4 + [False] * 6
        self.assertEqual(sound_cores(flags, 4), [(6, 10)])
        self.assertEqual(sound_cores([False] * 10, 4), [])


class TestPaddingOverlap(unittest.TestCase):
    def test_overlapping_keep_regions_are_clipped_disjoint(self):
        # 核心 (0,800)/(1120,1920),静音仅 320 采样,pre=post=400 → 保留区必然交叠
        segs = build_segments([(0, 10), (14, 24)], total_samples=24 * 80,
                              window_size=80, pre_pad=400, post_pad=400)
        a, b = segs
        self.assertEqual((a.start, a.end), (0, 960))    # 中点 (800+1120)//2
        self.assertEqual((b.start, b.end), (960, 1920))
        self.assertLessEqual(a.end, b.start)

    def test_no_duplicate_exported_samples(self):
        segs = build_segments([(0, 10), (14, 24), (30, 40)],
                              total_samples=40 * 80,
                              window_size=80, pre_pad=400, post_pad=400)
        seen = set()
        for s in segs:
            for x in range(s.start, s.end):
                self.assertNotIn(x, seen)
                seen.add(x)
        # 不相交且总长度守恒
        self.assertEqual(sum(s.duration for s in segs), len(seen))

    def test_stable_id_scheme(self):
        self.assertEqual(make_segment_id(0), "seg-00000000")
        self.assertEqual(make_segment_id(3200), "seg-00000c80")


class TestLatencyDeclaration(unittest.TestCase):
    def test_declared_latency_values(self):
        cfg = fixtures.base_config()
        self.assertEqual(cfg.confirmation_latency_samples,
                         cfg.min_silence + cfg.window_size)
        self.assertEqual(cfg.export_latency_samples,
                         cfg.confirmation_latency_samples + cfg.post_pad)


if __name__ == "__main__":
    unittest.main()
