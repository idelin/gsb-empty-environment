import math
import unittest
from pathlib import Path

from gaprepair import (MASK_KNOWN, MASK_REPAIRED, MASK_UNKNOWN,
                       NUMERIC_TOLERANCE, Mode, ModelRegistry, ModelVersion,
                       RepairConfig, find_gaps, load_fixture,
                       online_latency_samples, repair, restore_segment,
                       synthetic_occlusion_experiment)
from gaprepair.gaps import Gap

FIXTURES = Path(__file__).resolve().parent.parent / "fixtures"


def sine(n, fs=100.0):
    return [math.sin(2 * math.pi * 1.5 * i / fs)
            + 0.3 * math.sin(2 * math.pi * 7.0 * i / fs) for i in range(n)]


def with_gap(values, start, length):
    out = list(values)
    for i in range(start, start + length):
        out[i] = math.nan
    return out


class GapDetectionTests(unittest.TestCase):
    def test_true_zero_is_not_missing(self):
        values = [1.0, 0.0, 2.0, math.nan, 3.0]
        gaps = find_gaps(values)
        self.assertEqual([(g.start, g.length) for g in gaps], [(3, 1)])

    def test_nan_runs_merge(self):
        values = [math.nan, math.nan, 1.0, math.nan]
        gaps = find_gaps(values)
        self.assertEqual([(g.start, g.length) for g in gaps], [(0, 2), (3, 1)])


class RepairTests(unittest.TestCase):
    def setUp(self):
        self.config = RepairConfig(max_gap=8, ar_order=4, window=32,
                                   min_history=6)

    def test_short_gap_offline_linear(self):
        samples = with_gap(sine(60), 20, 4)
        result = repair(samples, 100.0, self.config, Mode.OFFLINE)
        segment = result.repairs[0]
        self.assertEqual(segment.method, "linear")
        self.assertEqual(result.mask[20:24], [MASK_REPAIRED] * 4)
        self.assertTrue(all(math.isfinite(v) for v in result.samples))
        self.assertEqual(len(segment.stderr), 4)

    def test_long_gap_remains_unknown(self):
        samples = with_gap(sine(100), 30, 15)
        result = repair(samples, 100.0, self.config, Mode.OFFLINE)
        self.assertEqual(result.repairs[0].method, "none")
        self.assertEqual(result.repairs[0].evidence["reason"],
                         "gap_exceeds_threshold")
        self.assertEqual(result.mask[30:45], [MASK_UNKNOWN] * 15)
        self.assertTrue(all(math.isnan(v) for v in result.samples[30:45]))

    def test_threshold_boundary(self):
        for length, expected in ((8, MASK_REPAIRED), (9, MASK_UNKNOWN)):
            samples = with_gap(sine(80), 30, length)
            result = repair(samples, 100.0, self.config, Mode.OFFLINE)
            self.assertEqual(result.mask[30], expected)

    def test_online_ar_uses_only_past(self):
        samples = with_gap(sine(90), 40, 4)
        full = repair(samples, 100.0, self.config, Mode.ONLINE)
        truncated = repair(samples[:44], 100.0, self.config, Mode.ONLINE)
        self.assertEqual(full.repairs[0].method, "ar")
        self.assertEqual(full.repairs[0].predictions,
                         truncated.repairs[0].predictions)

    def test_ill_conditioned_fit_degrades_with_evidence(self):
        samples = [1.0] * 40 + [math.nan] * 3 + [2.0] * 10
        result = repair(samples, 100.0, self.config, Mode.ONLINE)
        segment = result.repairs[0]
        self.assertEqual(segment.method, "ar")
        fit_evidence = segment.evidence["fit"]
        self.assertTrue(fit_evidence["degraded"])
        self.assertTrue(any(a["status"] == "rejected"
                            for a in fit_evidence["attempts"]))
        for pred in segment.predictions:
            self.assertAlmostEqual(pred, 1.0, places=9)

    def test_modes_are_not_conflated(self):
        samples = with_gap(sine(60), 20, 4)
        online = repair(samples, 100.0, self.config, Mode.ONLINE)
        offline = repair(samples, 100.0, self.config, Mode.OFFLINE)
        self.assertEqual(online.mode, Mode.ONLINE)
        self.assertEqual(offline.mode, Mode.OFFLINE)
        self.assertEqual(online.repairs[0].method, "ar")
        self.assertEqual(offline.repairs[0].method, "linear")
        self.assertNotEqual(online.repairs[0].predictions,
                            offline.repairs[0].predictions)

    def test_start_gap_never_two_sided_linear(self):
        samples = with_gap(sine(60), 0, 4)
        offline = repair(samples, 100.0, self.config, Mode.OFFLINE)
        self.assertNotEqual(offline.repairs[0].method, "linear")
        self.assertEqual(offline.repairs[0].method, "ar_backward")
        online = repair(samples, 100.0, self.config, Mode.ONLINE)
        self.assertEqual(online.repairs[0].method, "none")
        self.assertEqual(online.mask[0:4], [MASK_UNKNOWN] * 4)

    def test_end_gap_offline_uses_ar_not_linear(self):
        samples = with_gap(sine(60), 56, 4)
        offline = repair(samples, 100.0, self.config, Mode.OFFLINE)
        self.assertEqual(offline.repairs[0].method, "ar")

    def test_known_samples_unchanged(self):
        samples = with_gap(sine(60), 20, 4)
        for mode in (Mode.ONLINE, Mode.OFFLINE):
            result = repair(samples, 100.0, self.config, mode)
            for i, value in enumerate(samples):
                if not math.isnan(value):
                    self.assertEqual(result.samples[i], value)
                    self.assertEqual(result.mask[i], MASK_KNOWN)

    def test_true_zero_survives_repair(self):
        samples = with_gap(sine(60), 20, 4)
        samples[10] = 0.0
        result = repair(samples, 100.0, self.config, Mode.OFFLINE)
        self.assertEqual(result.samples[10], 0.0)
        self.assertEqual(result.mask[10], MASK_KNOWN)

    def test_repaired_samples_never_used_as_training(self):
        samples = with_gap(with_gap(sine(120), 30, 4), 60, 4)
        batch = repair(samples, 100.0, self.config, Mode.ONLINE)
        confirmed = [not math.isnan(v) for v in samples]
        seg_b = restore_segment(samples, confirmed, Gap(60, 4),
                                self.config, Mode.ONLINE)
        self.assertEqual(batch.repairs[1].predictions, seg_b.predictions)
        filled = list(batch.samples)
        seg_b2 = restore_segment(filled, confirmed, Gap(60, 4),
                                 self.config, Mode.ONLINE)
        self.assertEqual(seg_b2.predictions, seg_b.predictions)

    def test_segment_by_segment_matches_batch(self):
        samples = with_gap(with_gap(sine(90), 20, 3), 50, 5)
        for mode in (Mode.ONLINE, Mode.OFFLINE):
            batch = repair(samples, 100.0, self.config, mode)
            confirmed = [not math.isnan(v) for v in samples]
            for idx, gap in enumerate(find_gaps(samples)):
                seg = restore_segment(samples, confirmed, gap,
                                      self.config, mode)
                self.assertEqual(seg.predictions,
                                 batch.repairs[idx].predictions)
                self.assertEqual(seg.stderr, batch.repairs[idx].stderr)

    def test_error_estimates_present_and_ordered(self):
        samples = with_gap(sine(90), 40, 4)
        result = repair(samples, 100.0, self.config, Mode.ONLINE)
        stderr = result.repairs[0].stderr
        self.assertEqual(len(stderr), 4)
        self.assertTrue(all(s >= 0.0 for s in stderr))
        for k in range(len(stderr) - 1):
            self.assertGreaterEqual(stderr[k + 1],
                                    stderr[k] - NUMERIC_TOLERANCE)


class CalibrationTests(unittest.TestCase):
    def test_synthetic_occlusion_calibrates_confidence(self):
        config = RepairConfig(max_gap=8, ar_order=4, window=32)
        calib = synthetic_occlusion_experiment(
            sine(200), config, Mode.OFFLINE,
            gap_lengths=(2, 4), trials_per_length=10, seed=1)
        self.assertTrue(calib.scales)
        for scale in calib.scales.values():
            self.assertGreater(scale, 0.0)
        for detail in calib.details.values():
            self.assertGreater(detail["n"], 0)
            self.assertGreaterEqual(detail["coverage_95"], 0.0)
            self.assertLessEqual(detail["coverage_95"], 1.0)

    def test_calibration_scales_reported_stderr(self):
        config = RepairConfig(max_gap=8, ar_order=4, window=32)
        samples = with_gap(sine(200), 100, 4)
        plain = repair(samples, 100.0, config, Mode.OFFLINE)
        calib = synthetic_occlusion_experiment(
            samples, config, Mode.OFFLINE,
            gap_lengths=(4,), trials_per_length=10, seed=2)
        scaled = repair(samples, 100.0, config, Mode.OFFLINE,
                        calibration=calib)
        scale = scaled.repairs[0].evidence.get("calibration_scale", 1.0)
        for a, b in zip(plain.repairs[0].stderr, scaled.repairs[0].stderr):
            self.assertAlmostEqual(b, a * scale, places=12)


class RegistryTests(unittest.TestCase):
    def test_rollback_restores_previous_version(self):
        registry = ModelRegistry()
        registry.register(ModelVersion("v1", RepairConfig(max_gap=8)))
        registry.register(ModelVersion("v2", RepairConfig(max_gap=6)))
        self.assertEqual(registry.active.name, "v2")
        rolled = registry.rollback()
        self.assertEqual(rolled.name, "v1")
        self.assertEqual(registry.active.config.max_gap, 8)

    def test_rollback_to_named_version(self):
        registry = ModelRegistry()
        registry.register(ModelVersion("v1", RepairConfig()))
        registry.register(ModelVersion("v2", RepairConfig()))
        registry.register(ModelVersion("v3", RepairConfig()))
        self.assertEqual(registry.rollback("v1").name, "v1")
        self.assertEqual(registry.active.name, "v1")

    def test_rollback_without_history_fails(self):
        registry = ModelRegistry()
        registry.register(ModelVersion("v1", RepairConfig()))
        with self.assertRaises(LookupError):
            registry.rollback()


class DeclarationTests(unittest.TestCase):
    def test_latency_model(self):
        self.assertEqual(online_latency_samples(4), 5)
        self.assertEqual(online_latency_samples(1), 2)

    def test_numeric_tolerance_declared(self):
        self.assertEqual(NUMERIC_TOLERANCE, 1e-9)


class FixtureTests(unittest.TestCase):
    def test_fixture_loads_with_sample_rate(self):
        samples, fs = load_fixture(FIXTURES / "signal_short_gaps.json")
        self.assertEqual(fs, 100.0)
        self.assertEqual(len(samples), 120)
        self.assertEqual(samples[10], 0.0)
        self.assertEqual([(g.start, g.length) for g in find_gaps(samples)],
                         [(30, 3), (64, 6)])


if __name__ == "__main__":
    unittest.main()
