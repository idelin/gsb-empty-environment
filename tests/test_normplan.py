import json
import math
import os
import random
import tempfile
import unittest
from array import array

from normplan import fixtures
from normplan.apply import PlanInvalidError, apply_plan, apply_plan_to_file
from normplan.planner import (
    PEAK_TOLERANCE,
    RMS_TOLERANCE_DB,
    Plan,
    Segment,
    db_to_lin,
    input_digest,
    lin_to_db,
    plan_gains,
)


class InfeasibleAdjacentTest(unittest.TestCase):
    def test_strict_rate_limit_reports_conflict(self):
        samples, sr = fixtures.loud_quiet_tone()
        segs = [Segment(0, 4000, -40.0), Segment(4000, 8000, -6.0)]
        result = plan_gains(samples, sr, segs, peak_ceiling=1.0,
                            max_adjacent_ratio=1.05)
        self.assertFalse(result.normalized)
        kinds = [c.kind for c in result.conflicts]
        self.assertIn("infeasible_pair", kinds)
        pair = next(c for c in result.conflicts if c.kind == "infeasible_pair")
        self.assertEqual(pair.segments, [0, 1])
        self.assertEqual(pair.cause, "gain_rate_limit")
        self.assertGreater(pair.residual_db, 0.0)
        self.assertIn("rms_target", kinds)
        worst = max(abs(s.residual_db) for s in result.plan.segments)
        self.assertGreater(worst, RMS_TOLERANCE_DB)

    def test_relaxed_rate_limit_normalizes(self):
        samples, sr = fixtures.loud_quiet_tone()
        segs = [Segment(0, 4000, -30.0), Segment(4000, 8000, -12.0)]
        result = plan_gains(samples, sr, segs, peak_ceiling=0.98,
                            max_adjacent_ratio=4.0)
        self.assertTrue(result.normalized, result.to_dict()["conflicts"])
        for seg in result.plan.segments:
            self.assertLessEqual(abs(seg.residual_db), RMS_TOLERANCE_DB)


class CrossfadePeakTest(unittest.TestCase):
    def _setup(self, fade):
        samples, sr = fixtures.overlap_peak_tone()
        segs = [Segment(0, 2500, -3.0), Segment(2000, 4500, -3.0)]
        return samples, sr, plan_gains(samples, sr, segs, peak_ceiling=1.0,
                                       max_adjacent_ratio=4.0, fade=fade)

    def test_equal_power_crossfade_peak_not_missed(self):
        samples, sr, result = self._setup("equal_power")
        upper = 1.0 / 0.9
        naive_peak = 0.9 * math.sqrt(2.0) * upper
        self.assertGreater(naive_peak, 1.0)
        rendered_peak = max(
            abs(float(x)) * result.plan.gain_at(n)
            for n, x in enumerate(samples)
        )
        self.assertAlmostEqual(rendered_peak, result.output_peak, places=9)
        self.assertLessEqual(result.output_peak, 1.0 * (1.0 + PEAK_TOLERANCE))
        for seg in result.plan.segments:
            self.assertLess(seg.gain, upper)
        self.assertFalse(result.normalized)
        self.assertIn("rms_target", [c.kind for c in result.conflicts])

    def test_linear_fade_stays_under_ceiling(self):
        _, _, result = self._setup("linear")
        self.assertLessEqual(result.output_peak, 1.0 * (1.0 + PEAK_TOLERANCE))
        self.assertNotIn("crossfade_peak", [c.kind for c in result.conflicts])


class ChunkedApplyTest(unittest.TestCase):
    def test_random_chunks_match_whole_buffer(self):
        samples, sr = fixtures.noisy_segments()
        segs = [Segment(0, 2000, -12.0), Segment(1500, 4000, -6.0)]
        result = plan_gains(samples, sr, segs, peak_ceiling=0.95,
                            max_adjacent_ratio=3.0, fade="equal_power")
        whole = apply_plan(result.plan, samples)
        rng = random.Random(7)
        points = sorted(rng.sample(range(1, len(samples)), 63))
        bounds = [0] + points + [len(samples)]
        chunks = list(zip(bounds, bounds[1:]))
        chunked = apply_plan(result.plan, samples, chunks=chunks)
        self.assertEqual(whole.tobytes(), chunked.tobytes())

    def test_serialization_roundtrip_applies_identically(self):
        samples, sr = fixtures.noisy_segments()
        segs = [Segment(0, 2000, -12.0), Segment(1500, 4000, -6.0)]
        result = plan_gains(samples, sr, segs, peak_ceiling=0.95,
                            max_adjacent_ratio=3.0, fade="equal_power")
        restored = Plan.from_json(result.plan.to_json())
        self.assertEqual(result.plan.to_dict(), restored.to_dict())
        self.assertEqual(apply_plan(result.plan, samples).tobytes(),
                         apply_plan(restored, samples).tobytes())


class DigestInvalidationTest(unittest.TestCase):
    def test_source_correction_invalidates_plan(self):
        samples, sr = fixtures.loud_quiet_tone()
        segs = [Segment(0, 4000, -30.0), Segment(4000, 8000, -12.0)]
        result = plan_gains(samples, sr, segs)
        corrected = array("f", samples)
        corrected[100] = 0.123
        with self.assertRaises(PlanInvalidError):
            apply_plan(result.plan, corrected)
        with self.assertRaises(PlanInvalidError):
            apply_plan(result.plan, samples, sample_rate=sr * 2)
        apply_plan(result.plan, samples)

    def test_digest_is_stable(self):
        samples, sr = fixtures.loud_quiet_tone()
        self.assertEqual(input_digest(samples, sr),
                         input_digest(array("f", samples), sr))
        self.assertNotEqual(input_digest(samples, sr),
                            input_digest(samples, sr + 1))


class CancelWriteTest(unittest.TestCase):
    def setUp(self):
        samples, sr = fixtures.noisy_segments()
        self.samples, self.sr = samples, sr
        segs = [Segment(0, 2000, -12.0), Segment(1500, 4000, -6.0)]
        self.plan = plan_gains(samples, sr, segs).plan

    def test_cancel_publishes_no_complete_marker(self):
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "render")
            ok = apply_plan_to_file(self.plan, self.samples, out,
                                    chunk_size=500,
                                    cancel_check=lambda w, t: w >= 1000)
            self.assertFalse(ok)
            self.assertFalse(os.path.exists(out + ".complete"))
            self.assertTrue(os.path.exists(out + ".part"))
            self.assertLess(os.path.getsize(out + ".part"),
                            4 * len(self.samples))

    def test_full_write_publishes_marker_and_matches_whole_apply(self):
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "render")
            ok = apply_plan_to_file(self.plan, self.samples, out,
                                    chunk_size=333)
            self.assertTrue(ok)
            with open(out + ".complete", encoding="utf-8") as fh:
                marker = json.load(fh)
            self.assertTrue(marker["complete"])
            self.assertEqual(marker["input_digest"], self.plan.input_digest)
            with open(out + ".part", "rb") as fh:
                data = fh.read()
            self.assertEqual(data,
                             apply_plan(self.plan, self.samples).tobytes())


class SilenceAndShortSegmentTest(unittest.TestCase):
    def test_silence_is_not_amplified(self):
        samples, sr = fixtures.tone_with_silence()
        segs = [Segment(0, 2000, -12.0), Segment(2000, 4000, -12.0),
                Segment(4000, 6000, -12.0)]
        result = plan_gains(samples, sr, segs, peak_ceiling=0.95,
                            max_adjacent_ratio=8.0)
        silent = result.plan.segments[1]
        self.assertTrue(silent.silent)
        self.assertLessEqual(silent.gain, 1.0)
        self.assertEqual(silent.residual_db, 0.0)
        self.assertFalse(any(1 in c.segments for c in result.conflicts))

    def test_short_segment_uses_real_energy(self):
        samples, sr = fixtures.short_blip()
        segs = [Segment(1500, 1504, -6.0)]
        result = plan_gains(samples, sr, segs, peak_ceiling=10.0)
        seg = result.plan.segments[0]
        self.assertAlmostEqual(seg.rms, 0.4, places=5)
        expected = db_to_lin(-6.0) / seg.rms
        self.assertAlmostEqual(seg.gain, expected, places=6)
        self.assertTrue(result.normalized)
        self.assertAlmostEqual(lin_to_db(seg.achieved_rms), -6.0, places=1)


class OverlapEnergyTest(unittest.TestCase):
    def test_achieved_rms_uses_rendered_gain_curve(self):
        samples, sr = fixtures.overlap_peak_tone()
        segs = [Segment(0, 2500, -3.0), Segment(2000, 4500, -20.0)]
        result = plan_gains(samples, sr, segs, peak_ceiling=10.0,
                            max_adjacent_ratio=100.0, fade="linear")
        seg = result.plan.segments[0]
        naive = seg.gain * seg.rms
        self.assertLess(seg.achieved_rms, 0.98 * naive)
        total = 0.0
        for n in range(seg.start, seg.end):
            v = float(samples[n]) * result.plan.gain_at(n)
            total += v * v
        manual = math.sqrt(total / (seg.end - seg.start))
        self.assertAlmostEqual(seg.achieved_rms, manual, places=9)


if __name__ == "__main__":
    unittest.main()
