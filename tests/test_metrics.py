import math
import unittest

from filterverify.metrics import (
    IllConditionedError,
    estimate_lag,
    hann,
    tone_fit,
    windowed_tone_amplitude,
)
from filterverify.signals import multitone

FS = 8000.0


class ToneFitTest(unittest.TestCase):
    def test_recovers_amplitude_and_phase(self):
        n = 2048
        f = 1000.0
        x = [0.7 * math.cos(2 * math.pi * f * k / FS + 0.3) for k in range(n)]
        amp, phase, res = tone_fit(x, f, FS)
        self.assertAlmostEqual(amp, 0.7, places=9)
        self.assertAlmostEqual(phase, -0.3, places=9)  # phase = atan2(b, a) = -phi
        self.assertLess(res, 1e-9)

    def test_near_nyquist_tone_interpreted_correctly(self):
        # 3900 Hz = 0.4875 fs: close to Nyquist but representable; the fit
        # must recover the amplitude, not alias or blow up.
        n = 2048
        f = 0.4875 * FS
        x = [0.4 * math.cos(2 * math.pi * f * k / FS) for k in range(n)]
        amp, _, _ = tone_fit(x, f, FS)
        self.assertAlmostEqual(amp, 0.4, places=6)

    def test_exactly_nyquist_is_flagged_not_misread(self):
        n = 256
        f = FS / 2.0  # sin basis vanishes: amplitude/phase ambiguous
        x = [(-1.0) ** k for k in range(n)]
        with self.assertRaises((IllConditionedError, ValueError)):
            tone_fit(x, f, FS)

    def test_above_nyquist_rejected(self):
        with self.assertRaises(ValueError):
            tone_fit([0.0] * 64, 0.6 * FS, FS)


class WindowNormalizationTest(unittest.TestCase):
    def test_unity_gain_not_misread_as_attenuation(self):
        # A unity-amplitude tone through a Hann window must read 1.0 with
        # coherent-gain normalization (2*|X|/sum(w)), not 0.5 (2*|X|/N).
        n = 4096
        freqs = [200.0, 500.0, 900.0]
        x = multitone(freqs, FS, n, seed=7, amplitude=1.0)
        per_tone = 1.0 / len(freqs)
        for f in freqs:
            a = windowed_tone_amplitude(x, f, FS, hann(n))
            self.assertAlmostEqual(a, per_tone, delta=2e-3)
        # document the pitfall: naive 2|X|/N under-reads by the coherent gain
        f = freqs[0]
        w = hann(n)
        naive = windowed_tone_amplitude(x, f, FS, [1.0] * n)  # rect window
        self.assertAlmostEqual(naive, per_tone, delta=2e-3)
        self.assertAlmostEqual(sum(w) / n, 0.5, delta=1e-3)


class LagTest(unittest.TestCase):
    def test_estimate_lag(self):
        n = 512
        x = [math.sin(2 * math.pi * 300.0 * k / FS) + 0.5 * math.sin(2 * math.pi * 700.0 * k / FS)
             for k in range(n)]
        y = [0.0] * 9 + x
        lag, _ = estimate_lag(x, y, 32)
        self.assertEqual(lag, 9)


if __name__ == "__main__":
    unittest.main()
