import math
import unittest

from loudnorm import (
    GAIN_TOLERANCE_DB,
    PEAK_TOLERANCE,
    STATUS_NORMALIZED,
    STATUS_PARTIAL,
    CurveSegment,
    GainCurve,
    plan_normalization,
    region_rms,
)
from loudnorm import fixtures
from loudnorm.planner import _rendered_peak


class TestFeasiblePlan(unittest.TestCase):
    def setUp(self):
        self.samples, self.rate, self.specs, self.kwargs = \
            fixtures.feasible_case()
        self.plan = plan_normalization(
            self.samples, self.rate, self.specs, **self.kwargs)

    def test_status_normalized_without_conflicts(self):
        self.assertEqual(self.plan.status, STATUS_NORMALIZED)
        self.assertEqual(self.plan.conflicts, [])
        self.assertTrue(self.plan.is_normalized)

    def test_targets_met_within_tolerance(self):
        for seg in self.plan.segments:
            achieved = seg["gain"] * seg["rms"]
            residual_db = 20.0 * math.log10(achieved / seg["target_rms"])
            self.assertGreaterEqual(residual_db, -GAIN_TOLERANCE_DB)

    def test_rendered_peak_within_ceiling(self):
        curve = self.plan.gain_curve()
        peak, _ = _rendered_peak(self.samples, curve)
        self.assertLessEqual(peak, self.plan.peak_ceiling + PEAK_TOLERANCE)

    def test_latency_and_tolerances_declared(self):
        self.assertEqual(self.plan.latency_samples,
                         self.kwargs["fade_len"] // 2)
        self.assertIn("gain_db", self.plan.tolerances)
        self.assertIn("peak_linear", self.plan.tolerances)
        self.assertIn("silence_rms", self.plan.tolerances)
        self.assertIn("offline", self.plan.latency_note)


class TestSilence(unittest.TestCase):
    def test_silence_is_never_amplified(self):
        samples, rate, specs, kwargs = fixtures.silence_case()
        plan = plan_normalization(samples, rate, specs, **kwargs)
        seg0 = plan.segments[0]
        self.assertTrue(seg0["silent"])
        self.assertEqual(seg0["gain"], 1.0)
        curve = plan.gain_curve()
        # The silent region stays exactly zero after rendering.
        for n in range(0, specs[0].end - kwargs["fade_len"] // 2):
            self.assertEqual(samples[n] * curve.gain_at(n), 0.0)


class TestShortSegmentEnergy(unittest.TestCase):
    def test_short_segment_uses_true_energy(self):
        samples, rate, specs, kwargs = fixtures.short_segment_case()
        plan = plan_normalization(samples, rate, specs, **kwargs)
        seg = plan.segments[1]
        self.assertEqual(seg["end"] - seg["start"], 37)
        # True RMS of a partial sine cycle differs from amp / sqrt(2).
        naive = 0.1 / math.sqrt(2.0)
        self.assertNotAlmostEqual(seg["rms"], naive, places=4)
        self.assertAlmostEqual(
            seg["rms"], region_rms(samples, seg["start"], seg["end"]))
        # And the target is reached using that true energy.
        achieved = seg["gain"] * seg["rms"]
        residual_db = 20.0 * math.log10(achieved / seg["target_rms"])
        self.assertGreaterEqual(residual_db, -GAIN_TOLERANCE_DB)


class TestInfeasibleAdjacentTargets(unittest.TestCase):
    def test_rate_limit_conflict_reported_with_residual(self):
        samples, rate, specs, kwargs = fixtures.infeasible_case()
        plan = plan_normalization(samples, rate, specs, **kwargs)
        self.assertEqual(plan.status, STATUS_PARTIAL)
        self.assertFalse(plan.is_normalized)
        target_conflicts = [c for c in plan.conflicts
                            if c["kind"] == "target_not_reached"]
        self.assertTrue(target_conflicts)
        conflict = target_conflicts[0]
        self.assertEqual(conflict["segment"], 1)
        self.assertLess(conflict["residual_db"], -GAIN_TOLERANCE_DB)
        self.assertIn("gain_rate_limit_with_segment_0", conflict["causes"])
        # The rate limit itself is still respected by the reported curve.
        gains = [s["gain"] for s in plan.segments]
        self.assertLessEqual(abs(gains[1] - gains[0]),
                             kwargs["max_gain_step"] + 1e-9)


class TestPeakCeiling(unittest.TestCase):
    def test_ceiling_clamps_gain_and_reports_residual(self):
        samples = fixtures.join(fixtures.tone(300.0, 0.4, 0.8))
        specs = [fixtures.SegmentSpec(0, len(samples), 0.5)]
        plan = plan_normalization(samples, fixtures.SAMPLE_RATE, specs,
                                  peak_ceiling=0.5, max_gain_step=10.0)
        self.assertEqual(plan.status, STATUS_PARTIAL)
        seg = plan.segments[0]
        self.assertAlmostEqual(seg["gain"] * seg["peak"], 0.5, places=6)
        conflict = plan.conflicts[0]
        self.assertEqual(conflict["kind"], "target_not_reached")
        self.assertIn("peak_ceiling", conflict["causes"])
        self.assertLess(conflict["residual_db"], 0.0)


class TestCrossfadePeak(unittest.TestCase):
    def test_crossfade_region_can_peak_above_either_segment(self):
        # Hand-built curve: per-segment scaled peaks are both under the
        # ceiling, yet the crossfade between them is not.
        samples, rate, specs, kwargs = fixtures.crossfade_peak_case()
        boundary = specs[0].end
        curve = GainCurve(
            [CurveSegment(0, boundary, 8.0),
             CurveSegment(boundary, len(samples), 0.5)],
            fade_len=512,
        )
        seg_peak_0 = 8.0 * max(abs(x) for x in samples[:boundary])
        seg_peak_1 = 0.5 * max(abs(x) for x in samples[boundary:])
        self.assertLessEqual(seg_peak_0, 1.0)
        self.assertLessEqual(seg_peak_1, 1.0)
        rendered, pos = _rendered_peak(samples, curve)
        self.assertGreater(rendered, 1.0)
        # The offending sample lives in the crossfade region.
        self.assertLess(abs(pos - boundary), 256)

    def test_final_check_catches_and_repairs_crossfade_peak(self):
        samples, rate, specs, kwargs = fixtures.crossfade_peak_case()
        plan = plan_normalization(samples, rate, specs, **kwargs)
        curve = plan.gain_curve()
        rendered, _ = _rendered_peak(samples, curve)
        self.assertLessEqual(rendered,
                             plan.peak_ceiling + PEAK_TOLERANCE)
        self.assertTrue(any(n["kind"] == "peak_repair" for n in plan.notes))
        # Repair sacrificed the loudness target: reported, not hidden.
        self.assertEqual(plan.status, STATUS_PARTIAL)
        self.assertTrue(any(c["kind"] == "target_not_reached"
                            for c in plan.conflicts))


class TestValidation(unittest.TestCase):
    def test_segments_must_cover_signal(self):
        samples = fixtures.tone(100.0, 0.1, 0.5)
        with self.assertRaises(ValueError):
            plan_normalization(samples, fixtures.SAMPLE_RATE,
                               [fixtures.SegmentSpec(0, 10, 0.1)])

    def test_segments_must_be_contiguous(self):
        samples = fixtures.tone(100.0, 0.1, 0.5)
        n = len(samples)
        specs = [fixtures.SegmentSpec(0, n // 2, 0.1),
                 fixtures.SegmentSpec(n // 2 + 1, n, 0.1)]
        with self.assertRaises(ValueError):
            plan_normalization(samples, fixtures.SAMPLE_RATE, specs)


if __name__ == "__main__":
    unittest.main()
