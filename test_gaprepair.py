"""Unittest suite for the gaprepair package (Python 3.11, stdlib only)."""

import math
import random
import unittest
from dataclasses import replace

from gaprepair import (
    MODE_OFFLINE,
    MODE_ONLINE,
    NUMERICAL_TOLERANCE,
    ModelRegistry,
    ModelVersion,
    RepairConfig,
    calibrate_and_register,
    calibrate_error_scale,
    find_gaps,
    is_missing,
    repair_all,
    restore_segments,
    synthetic_occlusion_experiment,
)
from gaprepair import fixtures

NAN = float("nan")


def sine_signal(n=200, seed=1):
    rng = random.Random(seed)
    return [
        math.sin(2.0 * math.pi * 3.0 * i / 50.0)
        + 0.3 * math.sin(2.0 * math.pi * i / 17.0)
        + 0.01 * rng.gauss(0.0, 1.0)
        for i in range(n)
    ]


def with_gap(samples, start, length):
    out = list(samples)
    for i in range(start, start + length):
        out[i] = NAN
    return out


class GapDetectionTest(unittest.TestCase):
    def test_find_gaps(self):
        samples = [1.0, NAN, NAN, 2.0, NAN, 3.0]
        self.assertEqual(find_gaps(samples), [(1, 2), (4, 1)])

    def test_trailing_gap(self):
        self.assertEqual(find_gaps([1.0, NAN, NAN]), [(1, 2)])

    def test_real_zeros_are_not_missing(self):
        samples = [0.0, 0.0, 1.0, 0.0]
        self.assertEqual(find_gaps(samples), [])
        self.assertFalse(is_missing(0.0))
        result = repair_all(samples, RepairConfig())
        self.assertFalse(any(result.repair_mask))
        self.assertEqual(result.samples, samples)


class OnlineRepairTest(unittest.TestCase):
    def setUp(self):
        self.config = RepairConfig(mode=MODE_ONLINE, max_gap_len=8)

    def test_short_gap_repaired_with_mask_and_errors(self):
        samples = with_gap(sine_signal(200), 80, 4)
        result = repair_all(samples, self.config)
        (gap,) = result.gaps
        self.assertEqual(gap.status, "repaired")
        self.assertEqual(gap.method, "ar")
        self.assertEqual(sum(result.repair_mask), 4)
        for k in range(4):
            self.assertTrue(result.repair_mask[80 + k])
            self.assertIsNotNone(result.error_estimates[80 + k])
            self.assertGreater(result.error_estimates[80 + k], 0.0)

    def test_error_estimates_grow_with_horizon(self):
        samples = with_gap(sine_signal(200), 80, 4)
        result = repair_all(samples, self.config)
        errs = result.gaps[0].error_estimates
        self.assertTrue(all(b > a for a, b in zip(errs, errs[1:])))

    def test_long_gap_left_unknown(self):
        samples = with_gap(sine_signal(200), 80, 12)
        result = repair_all(samples, self.config)
        (gap,) = result.gaps
        self.assertEqual(gap.status, "left_unknown")
        self.assertIn("exceeds max_gap_len", gap.evidence["reason"])
        self.assertFalse(any(result.repair_mask))
        self.assertTrue(all(math.isnan(v) for v in result.samples[80:92]))

    def test_threshold_boundary(self):
        config = RepairConfig(mode=MODE_ONLINE, max_gap_len=4)
        base = sine_signal(200)
        samples = with_gap(with_gap(base, 40, 4), 100, 5)
        result = repair_all(samples, config)
        by_start = {g.start: g for g in result.gaps}
        self.assertNotEqual(by_start[40].status, "left_unknown")
        self.assertEqual(by_start[100].status, "left_unknown")

    def test_known_samples_never_modified(self):
        base = sine_signal(200)
        base[50] = 0.0
        samples = with_gap(with_gap(base, 80, 4), 120, 6)
        result = repair_all(samples, self.config)
        for i, v in enumerate(samples):
            if not math.isnan(v):
                self.assertEqual(result.samples[i], v)
        self.assertEqual(result.samples[50], 0.0)

    def test_input_list_not_mutated(self):
        samples = with_gap(sine_signal(200), 80, 4)
        snapshot = list(samples)
        repair_all(samples, self.config)
        self.assertEqual(len(samples), len(snapshot))
        for a, b in zip(samples, snapshot):
            self.assertTrue(a is b or a == b or (math.isnan(a) and math.isnan(b)))

    def test_no_future_leakage_online(self):
        base = sine_signal(240)
        samples_a = with_gap(base, 80, 4)
        samples_b = list(samples_a)
        for i in range(84, 240):
            if not math.isnan(samples_b[i]):
                samples_b[i] = samples_b[i] + 7.5
        result_a = repair_all(samples_a, self.config)
        result_b = repair_all(samples_b, self.config)
        for k in range(4):
            self.assertAlmostEqual(
                result_a.samples[80 + k], result_b.samples[80 + k],
                delta=NUMERICAL_TOLERANCE,
            )
        self.assertEqual(
            result_a.gaps[0].error_estimates, result_b.gaps[0].error_estimates
        )

    def test_offline_does_use_future_data(self):
        base = sine_signal(240)
        samples_a = with_gap(base, 80, 4)
        samples_b = list(samples_a)
        samples_b[84] += 7.5
        config = RepairConfig(mode=MODE_OFFLINE)
        result_a = repair_all(samples_a, config)
        result_b = repair_all(samples_b, config)
        self.assertGreater(
            abs(result_a.samples[80] - result_b.samples[80]), 1.0
        )

    def test_repaired_samples_not_used_as_training(self):
        base = sine_signal(160)
        samples = with_gap(with_gap(base, 50, 4), 70, 4)
        result = repair_all(samples, self.config)
        by_start = {g.start: g for g in result.gaps}
        self.assertEqual(by_start[50].status, "repaired")
        self.assertEqual(by_start[70].status, "repaired")
        confirmed_before_70 = 70 - 4
        self.assertEqual(
            by_start[70].evidence["train_samples_used"], confirmed_before_70
        )

    def test_ill_conditioned_constant_segment_degrades(self):
        samples = [1.0] * 60 + [NAN] * 3 + [1.0] * 20
        result = repair_all(samples, self.config)
        (gap,) = result.gaps
        self.assertEqual(gap.status, "degraded")
        self.assertEqual(gap.method, "linear")
        self.assertIn("degenerate", gap.evidence["degrade_reason"])
        self.assertEqual(gap.evidence["degraded_to"], "linear")
        self.assertEqual(result.samples[60:63], [1.0, 1.0, 1.0])

    def test_singular_fit_degrades_with_evidence(self):
        samples = [(-1.0) ** i for i in range(60)] + [NAN] * 3 + [0.5] * 20
        result = repair_all(samples, self.config)
        (gap,) = result.gaps
        self.assertEqual(gap.status, "degraded")
        self.assertIn("singular", gap.evidence["degrade_reason"])
        self.assertIn("condition_estimate", gap.evidence["ar_fit"])

    def test_degrade_to_unknown_without_right_anchor(self):
        samples = [1.0] * 60 + [NAN] * 3
        result = repair_all(samples, self.config)
        (gap,) = result.gaps
        self.assertEqual(gap.status, "left_unknown")
        self.assertEqual(gap.evidence["anchors"], {"left": True, "right": False})

    def test_invalid_mode_rejected(self):
        with self.assertRaises(ValueError):
            RepairConfig(mode="sideways")


class EdgeGapTest(unittest.TestCase):
    def setUp(self):
        self.samples, self.rate, _ = fixtures.load_fixture_with_edge_gaps()

    def test_leading_gap_never_two_sided_linear(self):
        offline = repair_all(self.samples, RepairConfig(mode=MODE_OFFLINE))
        leading = next(g for g in offline.gaps if g.start == 0)
        self.assertEqual(leading.status, "left_unknown")
        self.assertFalse(leading.evidence["anchors"]["left"])
        self.assertTrue(all(math.isnan(v) for v in offline.samples[0:4]))

    def test_leading_gap_online_has_no_history(self):
        online = repair_all(self.samples, RepairConfig(mode=MODE_ONLINE))
        leading = next(g for g in online.gaps if g.start == 0)
        self.assertEqual(leading.status, "left_unknown")

    def test_trailing_gap_online_repaired_offline_unknown(self):
        online = repair_all(self.samples, RepairConfig(mode=MODE_ONLINE))
        offline = repair_all(self.samples, RepairConfig(mode=MODE_OFFLINE))
        trailing_online = online.gaps[-1]
        trailing_offline = offline.gaps[-1]
        self.assertEqual(trailing_online.status, "repaired")
        self.assertEqual(trailing_online.method, "ar")
        self.assertEqual(trailing_offline.status, "left_unknown")
        self.assertFalse(trailing_offline.evidence["anchors"]["right"])


class OfflineRepairTest(unittest.TestCase):
    def test_two_sided_linear_is_exact(self):
        samples = sine_signal(60)
        samples = with_gap(samples, 10, 3)
        config = RepairConfig(mode=MODE_OFFLINE)
        result = repair_all(samples, config)
        left, right = samples[9], samples[13]
        for k in range(3):
            expected = left + (right - left) * (k + 1) / 4.0
            self.assertAlmostEqual(result.samples[10 + k], expected,
                                   delta=NUMERICAL_TOLERANCE)
        self.assertEqual(result.gaps[0].method, "linear")

    def test_mask_and_errors_aligned(self):
        samples, _, _ = fixtures.load_fixture_with_gaps()
        result = repair_all(samples, RepairConfig(mode=MODE_OFFLINE))
        for mask, err in zip(result.repair_mask, result.error_estimates):
            self.assertEqual(mask, err is not None)


class DeterminismTest(unittest.TestCase):
    def test_segment_by_segment_matches_all_at_once(self):
        samples, _, _ = fixtures.load_fixture_with_gaps()
        config = RepairConfig(mode=MODE_ONLINE)
        final, steps = restore_segments(samples, config)
        result = repair_all(samples, config)
        self.assertEqual(len(steps), len(result.gaps))
        for a, b in zip(final, result.samples):
            if math.isnan(a) and math.isnan(b):
                continue
            self.assertAlmostEqual(a, b, delta=NUMERICAL_TOLERANCE)

    def test_repeated_runs_identical(self):
        samples, _, _ = fixtures.load_fixture_with_gaps()
        config = RepairConfig(mode=MODE_ONLINE)
        first = repair_all(samples, config)
        second = repair_all(samples, config)
        self.assertEqual(first.samples, second.samples)
        self.assertEqual(first.error_estimates, second.error_estimates)


class CalibrationTest(unittest.TestCase):
    def setUp(self):
        self.clean, _ = fixtures.load_clean_fixture()
        self.config = RepairConfig(mode=MODE_ONLINE)
        self.train_occ = fixtures.calibration_occlusions(count=24, length=4, seed=7)
        self.val_occ = fixtures.calibration_occlusions(count=24, length=4, seed=99)

    def test_experiment_scores_against_truth(self):
        metrics = synthetic_occlusion_experiment(
            self.clean, self.config, self.train_occ
        )
        self.assertEqual(metrics["samples"], 24 * 4)
        self.assertIsNotNone(metrics["coverage"])
        self.assertGreaterEqual(metrics["coverage"], 0.0)
        self.assertLessEqual(metrics["coverage"], 1.0)

    def test_calibration_reaches_target_coverage(self):
        raw = replace(self.config, error_scale=1.0)
        scale, _ = calibrate_error_scale(
            self.clean, raw, self.train_occ, target_coverage=0.95
        )
        calibrated = replace(self.config, error_scale=scale)
        metrics = synthetic_occlusion_experiment(
            self.clean, calibrated, self.train_occ
        )
        self.assertGreaterEqual(metrics["coverage"], 0.95 - NUMERICAL_TOLERANCE)

    def test_registry_rollback(self):
        registry = ModelRegistry()
        v1 = ModelVersion("v1", 1.0, {})
        v2 = ModelVersion("v2", 2.0, {})
        registry.register(v1)
        registry.register(v2)
        self.assertEqual(registry.active.name, "v2")
        registry.rollback()
        self.assertEqual(registry.active.name, "v1")
        with self.assertRaises(RuntimeError):
            registry.rollback()
        registry.rollback("v2")
        self.assertEqual(registry.active.name, "v2")
        with self.assertRaises(KeyError):
            registry.rollback("nope")
        with self.assertRaises(ValueError):
            registry.register(ModelVersion("v2", 3.0, {}))

    def test_worse_candidate_is_rolled_back(self):
        registry = ModelRegistry()
        wide = ModelVersion("wide-v0", error_scale=1e6, metrics={})
        registry.register(wide)
        outcome = calibrate_and_register(
            self.clean, self.config, self.train_occ, self.val_occ,
            registry, "calibrated-v1", target_coverage=0.8,
        )
        self.assertTrue(outcome["rolled_back"])
        self.assertEqual(registry.active.name, "wide-v0")
        self.assertIn("calibrated-v1", [v.name for v in registry.versions])

class FixtureTest(unittest.TestCase):
    def test_fixture_provides_samples_and_rate(self):
        samples, rate = fixtures.load_clean_fixture()
        self.assertEqual(rate, fixtures.SAMPLE_RATE)
        self.assertEqual(len(samples), 400)
        self.assertEqual(samples[100], 0.0)
        self.assertEqual(samples[250], 0.0)


if __name__ == "__main__":
    unittest.main()
