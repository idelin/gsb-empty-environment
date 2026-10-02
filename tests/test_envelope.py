"""Envelope detection and gain smoothing, tested independently of the
static curve (separation of concerns)."""
import math
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import fixtures
from dyncurve import Segment, compile_curve
from dyncurve.envelope import (GainSmoother, PeakDetector, RmsDetector,
                               amplitude_to_db)

FS = fixtures.SAMPLE_RATE


class TestDetectors(unittest.TestCase):
    def test_peak_detector_attack_then_release(self):
        det = PeakDetector(FS, attack_ms=1.0, release_ms=50.0)
        for _ in range(480):
            env = det.update(1.0)
        self.assertGreater(env, 0.9)            # fast attack converges
        peak = env
        for _ in range(48):
            env = det.update(0.0)
        self.assertLess(env, peak)              # releasing
        self.assertGreater(env, 0.5 * peak)     # but slowly (50 ms)

    def test_peak_detector_tracks_envelope_not_waveform(self):
        det = PeakDetector(FS, attack_ms=1.0, release_ms=50.0)
        sig = fixtures.sine_wave(1000.0, 0.05, 0.5)
        envs = [det.update(abs(s)) for s in sig]
        self.assertAlmostEqual(envs[-1], 0.5, delta=0.05)

    def test_rms_detector(self):
        det = RmsDetector(FS, window_ms=10.0)
        sig = fixtures.sine_wave(440.0, 0.1, 0.8)
        envs = [det.update(abs(s)) for s in sig]
        self.assertAlmostEqual(envs[-1], 0.8 / math.sqrt(2.0), delta=0.02)

    def test_detector_state_roundtrip(self):
        det = PeakDetector(FS, 1.0, 50.0)
        for s in fixtures.noise(0.01):
            det.update(abs(s))
        snap = det.get_state()
        a = [det.update(0.7) for _ in range(16)]
        det.set_state(snap)
        b = [det.update(0.7) for _ in range(16)]
        self.assertEqual(a, b)

    def test_amplitude_to_db_clamps_silence(self):
        self.assertTrue(math.isfinite(amplitude_to_db(0.0)))
        self.assertEqual(amplitude_to_db(0.0), -160.0)
        self.assertAlmostEqual(amplitude_to_db(1.0), 0.0)


class TestGainSmoother(unittest.TestCase):
    def test_smoothing_is_gradual_and_converges(self):
        sm = GainSmoother(FS, attack_ms=1.0, release_ms=20.0)
        vals = [sm.update(-12.0) for _ in range(960)]   # 20 time constants
        self.assertLess(vals[0], 0.0)                 # starts moving at once
        self.assertGreater(vals[0], -1.0)             # but no jump
        self.assertAlmostEqual(vals[-1], -12.0, places=3)
        back = [sm.update(0.0) for _ in range(int(FS * 0.4))]  # 20 release taus
        self.assertAlmostEqual(back[-1], 0.0, places=3)

    def test_curve_is_not_part_of_envelope(self):
        # separation: the static curve has no state and no detector coupling
        curve = compile_curve([Segment(-20.0, 4.0)])
        g1 = curve.gain_db(-10.0)
        det = PeakDetector(FS, 1.0, 50.0)
        det.update(0.9)
        g2 = curve.gain_db(-10.0)
        self.assertEqual(g1, g2)


if __name__ == "__main__":
    unittest.main()
