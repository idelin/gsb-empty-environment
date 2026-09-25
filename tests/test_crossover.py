"""分频器:旁路重组还原、群延迟补偿对齐、相位为纯延迟。"""
import math
import unittest

import fixtures
from dsp import Block, Crossover
from tests.util import argmax_abs, goertzel, max_abs_err


class TestBypassReconstruction(unittest.TestCase):
    def setUp(self):
        self.fs = fixtures.SAMPLE_RATE
        self.coeffs = fixtures.crossover_coefficients()

    def test_multitone_reconstruction(self):
        n = 4096
        tones = [(150.0, 0.2), (800.0, 0.15), (2500.0, 0.1), (6000.0, 0.1)]
        data = fixtures.multitone(n, tones)
        xo = Crossover(self.fs, 2, self.coeffs, fixtures.bypass_band_params())
        out, tel = xo.process(Block(data))
        d = tel["latency_samples"]
        self.assertEqual(d, fixtures.CROSSOVER_LATENCY)
        for c in range(2):
            err = max_abs_err(out.channels[c][d:], data[c][:n - d])
            self.assertLess(err, fixtures.PR_TOLERANCE)

    def test_group_delay_alignment(self):
        n = 1024
        t0 = 500
        data = fixtures.impulse(n, t0)
        xo = Crossover(self.fs, 2, self.coeffs, fixtures.bypass_band_params())
        out, tel = xo.process(Block(data), collect_bands=True)
        # 三个频带声明的群延迟确实不同
        self.assertEqual(tel["band_group_delays"],
                         list(fixtures.CROSSOVER_BAND_GROUP_DELAYS))
        # 补偿后各带脉冲峰值落在同一样本
        for band_out in tel["band_outputs"]:
            self.assertEqual(argmax_abs(band_out[0]), t0 + 90)
        self.assertEqual(argmax_abs(out.channels[0]), t0 + 90)
        self.assertAlmostEqual(out.channels[0][t0 + 90], 1.0,
                               delta=fixtures.PR_TOLERANCE)

    def test_phase_pure_delay(self):
        fs = self.fs
        a = 1024
        N = 2048
        n = a + N + 200
        ks = [13, 43, 214]  # 精确 DFT  bin,分别落在低/中/高带
        tones = [(k * fs / N, 0.2) for k in ks]
        data = fixtures.multitone(n, tones)
        xo = Crossover(fs, 2, self.coeffs, fixtures.bypass_band_params())
        out, tel = xo.process(Block(data))
        d = tel["latency_samples"]
        for (f, amp) in tones:
            ph_in, mag_in = goertzel(data[0], a, N, f, fs)
            ph_out, mag_out = goertzel(out.channels[0], a + d, N, f, fs)
            dphi = (ph_out - ph_in + math.pi) % (2.0 * math.pi) - math.pi
            self.assertAlmostEqual(dphi, 0.0,
                                   delta=fixtures.PHASE_TOLERANCE_RAD)
            self.assertAlmostEqual(mag_in, amp, delta=1e-3)
            self.assertAlmostEqual(mag_out, amp, delta=1e-3)


if __name__ == "__main__":
    unittest.main()
