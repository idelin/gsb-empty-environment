"""Hot rule reload: crossfade starts at the scheduled sample, total latency
never changes, gains never jump, envelope state is PRESERVED (not migrated)
across the swap -- all verified here."""
import math
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import fixtures
from dyncurve import (LATENCY_SAMPLES, TOLERANCES, DetectorConfig,
                      DynamicsProcessor, Segment, SidechainConfig,
                      SmootherConfig, compile_curve)

FS = fixtures.SAMPLE_RATE
JUMP_TOL = TOLERANCES["hot_swap_gain_db"]


def old_curve():
    return compile_curve([Segment(-30.0, 8.0)])


def new_curve():
    return compile_curve([Segment(-12.0, 2.0, knee_db=4.0)])


def transparent_proc(curve):
    # zero-time-constant detector+smoother: envelope settles instantly and
    # applied gain == curve gain each sample, so the crossfade ramp can be
    # checked directly against the static curves
    return DynamicsProcessor(FS, 1, curve,
                             detector=DetectorConfig("peak", 0.0, 0.0),
                             smoother=SmootherConfig(0.0, 0.0),
                             sidechain=SidechainConfig("max"))


def const_signal(n, amp=0.5):
    return [amp] * n


class TestHotSwap(unittest.TestCase):
    def setUp(self):
        self.n = 1500
        self.k = 600           # swap starts exactly here
        self.fade = 120
        self.sig = const_signal(self.n)

    def run_swap(self):
        proc = transparent_proc(old_curve())
        proc.set_curve(new_curve(), at_sample=self.k, fade_samples=self.fade)
        return proc, proc.process_block([self.sig])[0]

    def test_output_before_swap_point_is_unaffected(self):
        baseline = transparent_proc(old_curve())
        ref = baseline.process_block([self.sig])[0]
        _, out = self.run_swap()
        self.assertEqual(ref[:self.k], out[:self.k])

    def test_crossfade_matches_scheduled_blend(self):
        _, out = self.run_swap()
        level = 20.0 * math.log10(0.5)
        g_old, g_new = old_curve().gain_db(level), new_curve().gain_db(level)
        self.assertNotAlmostEqual(g_old, g_new, places=3)  # curves really differ
        # envelope settled well before K (constant input since sample 0)
        for i in (self.k, self.k + self.fade // 2, self.k + self.fade - 1,
                  self.k + self.fade + 10):
            pos = i - self.k
            a = min(max((pos + 1) / self.fade, 0.0), 1.0)
            expected_db = (1.0 - a) * g_old + a * g_new
            got_db = 20.0 * math.log10(out[i] / self.sig[i])
            self.assertAlmostEqual(got_db, expected_db, places=9)

    def test_no_gain_jump_at_swap_boundary(self):
        _, out = self.run_swap()
        gains = [20.0 * math.log10(out[i] / self.sig[i]) for i in range(self.n)]
        steps = [abs(b - a) for a, b in zip(gains, gains[1:])]
        # the crossfade is a linear dB ramp: the largest per-sample step
        # anywhere (incl. the swap boundary) is exactly the ramp slope --
        # i.e. there is no discontinuity beyond the scheduled transition
        level = 20.0 * math.log10(0.5)
        ramp = abs(new_curve().gain_db(level) - old_curve().gain_db(level)) / self.fade
        self.assertLessEqual(max(steps), ramp + JUMP_TOL)
        # concretely: the step at the boundary sample K equals the ramp step
        self.assertAlmostEqual(steps[self.k - 1], ramp, delta=JUMP_TOL)

    def test_latency_unchanged_by_swap(self):
        proc = transparent_proc(old_curve())
        self.assertEqual(proc.latency_samples, LATENCY_SAMPLES)
        proc.set_curve(new_curve(), at_sample=10, fade_samples=10)
        proc.process_block([self.sig])
        self.assertEqual(proc.latency_samples, LATENCY_SAMPLES)

    def test_envelope_state_is_preserved_not_migrated(self):
        # documented policy: detectors/smoothers keep their state across a
        # curve swap; only the static curve (plus crossfade) changes
        baseline = transparent_proc(old_curve())
        baseline.process_block([self.sig])
        proc, _ = self.run_swap()
        self.assertEqual(baseline.get_state()["detectors"],
                         proc.get_state()["detectors"])
        # and the smoother state follows the blended gain, not a reset
        self.assertNotEqual(proc.get_state()["smoothers"], [0.0])

    def test_final_curve_active_after_fade(self):
        proc, out = self.run_swap()
        self.assertIs(proc.curve, proc.get_state()["curve"])
        level = 20.0 * math.log10(0.5)
        got_db = 20.0 * math.log10(out[-1] / self.sig[-1])
        self.assertAlmostEqual(got_db, new_curve().gain_db(level), places=9)

    def test_queued_swaps_run_sequentially(self):
        third = compile_curve([Segment(-40.0, 1.0)])  # identity
        proc = transparent_proc(old_curve())
        proc.set_curve(new_curve(), at_sample=250, fade_samples=50)
        proc.set_curve(third, at_sample=300, fade_samples=50)  # queued
        out = proc.process_block([self.sig])[0]
        self.assertIs(proc.curve, third)
        # identity curve at the end -> output equals input
        self.assertAlmostEqual(out[-1], self.sig[-1], places=12)

    def test_swap_scheduled_in_the_past_starts_immediately(self):
        proc = transparent_proc(old_curve())
        proc.process_block([const_signal(500)])
        proc.set_curve(new_curve(), at_sample=100, fade_samples=50)
        out = proc.process_block([const_signal(500)])[0]
        level = 20.0 * math.log10(0.5)
        got_db = 20.0 * math.log10(out[-1] / 0.5)
        self.assertAlmostEqual(got_db, new_curve().gain_db(level), places=9)


if __name__ == "__main__":
    unittest.main()
