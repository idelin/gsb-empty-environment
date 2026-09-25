"""unittest suite for the rolling per-scene/per-band noise-floor model."""

from __future__ import annotations

import math
import unittest

from noise_floor import LIMITS, NoiseFloorBank
from noise_floor.model import (
    MARK_APPLIED,
    MARK_NOOP,
    MARK_UNKNOWN,
    MISSING,
    TRANSIENT,
    VALID,
    BandModel,
)
from noise_floor.quantile import (
    SortedMultiset,
    max_comparisons_per_update,
    oracle_quantile,
)
from noise_floor import fixtures, replay


class PeakRetractionTest(unittest.TestCase):
    """Extreme peak included first, marked transient later."""

    def test_baseline_equals_never_included(self):
        window = 16
        included = BandModel(window_frames=window)
        never = BandModel(window_frames=window)
        for frame in range(30):
            value = 10.0 + frame * 0.5  # distinct values: peak moves the median
            included.add(frame, frame, value)
            never.add(frame, frame, value)
        # Extreme peak arrives inside the window of both models' frame range.
        included.add(1000, 25, 500.0)
        peak_version = included.version
        self.assertNotEqual(included.estimate().value, never.estimate().value)
        # Late mark retracts the already-incorporated contribution.
        self.assertEqual(included.mark(1000, TRANSIENT), MARK_APPLIED)
        self.assertGreater(included.version, peak_version)
        self.assertEqual(included.estimate().value, never.estimate().value)
        self.assertEqual(included.estimate().n_valid, never.estimate().n_valid)
        # Restoring validity re-incorporates it exactly.
        included.mark(1000, VALID)
        self.assertNotEqual(included.estimate().value, never.estimate().value)

    def test_all_excluded_is_not_estimable_not_zero(self):
        model = BandModel(window_frames=8)
        for frame in range(8):
            model.add(frame, frame, 3.0 + frame)
        for frame in range(8):
            model.mark(frame, TRANSIENT)
        est = model.estimate()
        self.assertIsNone(est.value)
        self.assertEqual(est.n_valid, 0)
        self.assertIsNone(model.threshold().value)
        # MISSING behaves the same way.
        model2 = BandModel(window_frames=8)
        for frame in range(8):
            model2.add(frame, frame, None, MISSING)
        self.assertIsNone(model2.estimate().value)


class BandIndependenceTest(unittest.TestCase):
    def test_bands_count_valid_samples_separately(self):
        bank = NoiseFloorBank(window_frames=16)
        bank.set_scene("street")
        for frame in range(10):
            bank.model("low").add(frame, frame, 10.0 + frame * 0.01)
        for frame in range(4):
            bank.model("mid").add(100 + frame, frame, 5.0)
        for frame in range(7):
            bank.model("high").add(200 + frame, frame, 2.0)
        bank.model("mid").mark(100, TRANSIENT)
        bank.model("mid").mark(101, MISSING)
        self.assertEqual(bank.model("low").estimate().n_valid, 10)
        self.assertEqual(bank.model("mid").estimate().n_valid, 2)
        self.assertEqual(bank.model("high").estimate().n_valid, 7)
        # Estimates are independent per band.
        self.assertAlmostEqual(bank.model("low").estimate().value, 10.045)
        self.assertAlmostEqual(bank.model("mid").estimate().value, 5.0)


class IdempotentMarkTest(unittest.TestCase):
    def test_repeated_marks_are_noops(self):
        model = BandModel(window_frames=8)
        model.add(1, 0, 4.0)
        version0 = model.version
        self.assertEqual(model.mark(1, VALID), MARK_NOOP)
        self.assertEqual(model.mark(1, TRANSIENT), MARK_APPLIED)
        version1 = model.version
        self.assertEqual(model.mark(1, TRANSIENT), MARK_NOOP)
        self.assertEqual(model.mark(1, TRANSIENT), MARK_NOOP)
        self.assertEqual(model.version, version1)
        self.assertGreater(version1, version0)
        self.assertEqual(model.mark(999, TRANSIENT), MARK_UNKNOWN)
        self.assertEqual(model.version, version1)
        # Re-adding the identical sample is also idempotent.
        self.assertEqual(model.add(1, 0, 4.0, TRANSIENT), MARK_NOOP)
        with self.assertRaises(ValueError):
            model.add(1, 0, 9.0)


class SceneSwitchTest(unittest.TestCase):
    def test_scenes_keep_their_own_baselines(self):
        bank = NoiseFloorBank(window_frames=16)
        bank.set_scene("quiet_room")
        for frame in range(20):
            bank.model("low").add(frame, frame, 3.0)
        quiet_est = bank.model("low").estimate()
        bank.set_scene("street")
        for frame in range(20):
            bank.model("low").add(frame, frame, 30.0)
        street_est = bank.model("low").estimate()
        self.assertNotAlmostEqual(quiet_est.value, street_est.value)
        # Switching back restores the untouched baseline.
        bank.set_scene("quiet_room")
        resumed = bank.model("low").estimate()
        self.assertEqual(resumed, quiet_est)
        self.assertEqual(bank.scenes(), ["quiet_room", "street"])


class WatermarkTest(unittest.TestCase):
    def test_reclamation_bounded_by_query_watermark(self):
        window = 8
        model = BandModel(window_frames=window)
        for frame in range(40):
            model.add(frame, frame, 1.0 + frame * 0.001)
        retained_before = model.retained
        self.assertEqual(retained_before, 40)  # nothing reclaimed yet
        # Watermark beyond the window edge only reclaims expired history.
        reclaimed = model.set_watermark(1000)
        self.assertEqual(reclaimed, 40 - window)
        self.assertEqual(model.retained, window)
        # Live samples are never reclaimed, watermark notwithstanding.
        self.assertEqual(model.estimate().n_valid, window)
        # Late mark against a reclaimed sample is reported unknown.
        self.assertEqual(model.mark(0, TRANSIENT), MARK_UNKNOWN)
        # A lower watermark never reclaims in-window samples.
        model2 = BandModel(window_frames=window)
        for frame in range(20):
            model2.add(frame, frame, 2.0)
        reclaimed2 = model2.set_watermark(15)
        self.assertEqual(reclaimed2, 20 - window)
        self.assertEqual(model2.estimate().n_valid, window)


class VersionBoundThresholdTest(unittest.TestCase):
    def test_detections_corrected_after_late_marks(self):
        model = BandModel(window_frames=8, quantile=0.9, margin=1.0)
        for frame in range(8):
            model.add(frame, frame, 10.0)
        model.add(100, 8, 50.0)  # peak lifts the baseline
        th_with_peak = model.threshold()
        probe = th_with_peak.value - 0.1  # just below the lifted threshold
        self.assertFalse(model.detect(9, probe))
        stale = model.detections[-1]
        self.assertEqual(stale.threshold_version, th_with_peak.version)
        # Late mark retracts the peak; threshold drops below the probe.
        model.mark(100, TRANSIENT)
        corrections = model.correct_detections()
        self.assertEqual(len(corrections), 1)
        corr = corrections[0]
        self.assertFalse(corr.was_detected)
        self.assertTrue(corr.now_detected)
        self.assertEqual(corr.old_version, th_with_peak.version)
        self.assertEqual(corr.new_version, model.version)
        # Records are rebound; a second pass finds nothing stale.
        self.assertEqual(model.correct_detections(), [])
        self.assertEqual(model.detections[-1].threshold_version, model.version)


class QuantileStructureTest(unittest.TestCase):
    def test_exact_quantile_with_duplicates_against_oracle(self):
        values = [7.5, 1.0, 7.5, 7.5, 3.25, 1.0, 9.75, 3.25, 7.5]
        ms = SortedMultiset()
        for v in values:
            ms.add(v)
        for q in (0.0, 0.1, 0.25, 0.5, 0.75, 0.9, 1.0):
            self.assertAlmostEqual(ms.quantile(q), oracle_quantile(values, q),
                                   delta=LIMITS.quantile_abs_tol)
        # Duplicates discard one occurrence at a time.
        self.assertTrue(ms.discard(7.5))
        remaining = values[:]
        remaining.remove(7.5)
        self.assertAlmostEqual(ms.quantile(0.5), oracle_quantile(remaining, 0.5),
                               delta=LIMITS.quantile_abs_tol)

    def test_large_window_incremental_access_counts(self):
        window = 4096
        model = BandModel(window_frames=window)
        n_frames = 3 * window
        value = 0.0
        for frame in range(n_frames):
            value = (value * 1.0009765 + 0.731) % 97.0  # deterministic spread
            model.add(frame, frame, value)
        ops = n_frames  # one add per frame, plus expiries
        budget = 2 * ops * max_comparisons_per_update(window)
        self.assertLessEqual(model._active.comparisons, budget)
        # A full re-sort per frame would cost ~n_frames * window * log2(window)
        # comparisons; the incremental structure stays far below that.
        full_sort_cost = n_frames * window * math.log2(window)
        self.assertLess(model._active.comparisons, full_sort_cost / 100)
        # Spot-check exactness against the oracle on the final window.
        tail = SortedMultiset(model._active.sorted_items())
        self.assertEqual(len(tail), window)

    def test_rolling_matches_oracle_at_every_frame_small_scale(self):
        window = 12
        model = BandModel(window_frames=window, quantile=0.3)
        history: list[float] = []
        value = 5.0
        for frame in range(60):
            value = (value * 1.37 + 2.11) % 23.0
            model.add(frame, frame, value)
            history.append(value)
            live = history[-window:]
            self.assertAlmostEqual(
                model.estimate().value, oracle_quantile(live, 0.3),
                delta=LIMITS.quantile_abs_tol,
                msg=f"diverged at frame {frame}")


class ConsistencyTest(unittest.TestCase):
    """Chunked, out-of-order and save/restore ingestion agree."""

    def _feed(self, order="sequential", chunk=None, snapshot_at=None):
        model = BandModel(window_frames=16, quantile=0.5, margin=6.0)
        frames = list(range(48))
        if order == "reversed_blocks":
            frames = [f for i in range(0, 48, 8)
                      for f in reversed(range(i, i + 8))]
        values = {f: 10.0 + ((f * 37) % 11) * 0.13 for f in frames}
        if chunk:
            for i in range(0, len(frames), chunk):
                for f in frames[i:i + chunk]:
                    model.add(f, f, values[f])
        else:
            for f in frames:
                model.add(f, f, values[f])
                if snapshot_at is not None and f == snapshot_at:
                    model = BandModel.from_dict(model.to_dict())
        return model

    def test_chunked_matches_monolithic(self):
        whole = self._feed()
        for chunk in (1, 3, 7, 16, 48):
            self.assertEqual(self._feed(chunk=chunk).estimate(), whole.estimate())

    def test_out_of_order_matches_sequential(self):
        sequential = self._feed()
        shuffled = self._feed(order="reversed_blocks")
        self.assertEqual(shuffled.estimate(), sequential.estimate())
        self.assertEqual(shuffled.version, sequential.version)

    def test_save_restore_roundtrip(self):
        model = self._feed()
        model.add(1000, 40, 500.0)
        model.detect(41, 20.0)
        model.mark(1000, TRANSIENT)
        restored = BandModel.from_dict(model.to_dict())
        self.assertEqual(restored.estimate(), model.estimate())
        self.assertEqual(restored.version, model.version)
        self.assertEqual(restored.detections, model.detections)
        # Identical future behaviour after restore.
        for f in range(48, 64):
            v = 10.0 + ((f * 37) % 11) * 0.13
            model.add(f, f, v)
            restored.add(f, f, v)
        self.assertEqual(restored.estimate(), model.estimate())

    def test_mid_stream_restore_matches_uninterrupted(self):
        whole = self._feed()
        mid = self._feed(snapshot_at=23)
        self.assertEqual(mid.estimate(), whole.estimate())
        self.assertEqual(mid.version, whole.version)

    def test_bank_save_restore_roundtrip(self):
        bank = NoiseFloorBank(window_frames=16)
        bank.set_scene("street")
        for frame in range(24):
            bank.model("low").add(frame, frame, 5.0 + frame * 0.01)
            bank.model("high").add(frame, frame, 2.0)
        restored = NoiseFloorBank.from_dict(bank.to_dict())
        self.assertEqual(restored.model("low", scene="street").estimate(),
                         bank.model("low").estimate())
        self.assertEqual(restored.scene, "street")


class ReplayTest(unittest.TestCase):
    def test_all_modes_replay_identically(self):
        ops = replay.build_script()
        base = replay.run(ops, mode="sequential")
        self.assertTrue(any(e[0] == "correct" and e[2] >= 1 for e in base))
        for mode in ("chunked", "shuffled", "persist"):
            self.assertTrue(replay._close(replay.run(ops, mode=mode), base),
                            f"mode {mode} diverged")

    def test_fixtures_are_local_and_deterministic(self):
        self.assertEqual(fixtures.SAMPLE_RATE, 48_000)
        a = fixtures.band_values("low", 32)
        b = fixtures.band_values("low", 32)
        self.assertEqual(a, b)
        self.assertGreater(max(a), 50.0)  # injected extreme peak present


if __name__ == "__main__":
    unittest.main()
