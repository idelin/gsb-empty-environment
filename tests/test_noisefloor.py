import math
import random
import unittest

from noisefloor import (
    MISSING,
    TOL,
    TRANSIENT,
    VALID,
    NoiseFloorModel,
)
from noisefloor import fixtures


class SortOracle:
    """Independent full-sort oracle; used only at small scale, by design."""

    def __init__(self, window, q):
        self.window = window
        self.q = q
        self.samples = {}  # frame -> (value, status)

    def ingest(self, frame, value, status):
        if frame not in self.samples:  # duplicate ingest is a no-op
            self.samples[frame] = (value, status)

    def mark(self, frame, status):
        if frame not in self.samples:
            return
        value, old = self.samples[frame]
        if old == status:
            return
        if status == VALID and value is None:
            return
        self.samples[frame] = (value, status)

    def estimate(self):
        valid = sorted(f for f, (_, s) in self.samples.items() if s == VALID)
        vals = sorted(self.samples[f][0] for f in valid[-self.window:])
        if not vals:
            return None
        n = len(vals)
        h = (n - 1) * self.q
        lo = int(h)
        frac = h - lo
        if frac == 0.0:
            return vals[lo]
        return vals[lo] + (vals[lo + 1] - vals[lo]) * frac


class TestPeakRetraction(unittest.TestCase):
    def test_peak_retracted_equals_never_included(self):
        stream = fixtures.make_stream()
        peak = fixtures.PEAK_FRAME, fixtures.PEAK_BAND

        included = NoiseFloorModel(window=16, quantile=0.2)
        included.ingest_chunk(stream)
        # late mark: the extreme peak is declared transient *after* inclusion
        self.assertTrue(included.mark(peak[0], peak[1], TRANSIENT,
                                      scene=fixtures.scene_of(peak[0], 96)))

        never = NoiseFloorModel(window=16, quantile=0.2)
        never.ingest_chunk(s for s in stream
                           if not (s.frame == peak[0] and s.band == peak[1]))

        for scene in fixtures.SCENES:
            for band in fixtures.BANDS:
                a = included.estimate(band, scene)
                b = never.estimate(band, scene)
                self.assertEqual(a.estimable, b.estimable)
                if a.estimable:
                    self.assertEqual(a.value, b.value)  # bit-identical
                    self.assertEqual(a.n_valid, b.n_valid)

    def test_all_excluded_is_not_estimable_not_zero(self):
        m = NoiseFloorModel(window=8, quantile=0.5)
        for f in range(6):
            m.ingest(f, "low", 1.0 + f * 0.01)
        for f in range(6):
            self.assertTrue(m.mark(f, "low", TRANSIENT))
        est = m.estimate("low")
        self.assertFalse(est.estimable)
        self.assertIsNone(est.value)
        self.assertNotEqual(est.value, 0)
        self.assertEqual(est.n_valid, 0)
        # a band that only ever saw missing samples is likewise not estimable
        m.ingest(100, "mid", None, MISSING)
        self.assertFalse(m.estimate("mid").estimable)
        # detection on a non-estimable band is undecided, never a 0-baseline
        det = m.detect("low", 5.0)
        self.assertIsNone(det.decision)


class TestBandsAndMarks(unittest.TestCase):
    def test_bands_count_valid_samples_separately(self):
        m = NoiseFloorModel(window=64, quantile=0.2)
        for f in range(10):
            m.ingest(f, "low", 1.0)
            m.ingest(f, "mid", 2.0)
        for f in range(3):
            m.ingest(f, "high", 3.0)
        m.ingest(3, "high", None, MISSING)
        m.ingest(4, "high", None, MISSING)
        for f in range(4):
            m.mark(f, "mid", TRANSIENT)
        self.assertEqual(m.valid_count("low"), 10)
        self.assertEqual(m.valid_count("mid"), 6)
        self.assertEqual(m.valid_count("high"), 3)
        self.assertEqual(m.estimate("low").n_valid, 10)
        self.assertEqual(m.estimate("mid").n_valid, 6)
        self.assertEqual(m.estimate("high").n_valid, 3)

    def test_duplicate_marks_are_idempotent(self):
        m = NoiseFloorModel(window=8, quantile=0.5)
        for f in range(8):
            m.ingest(f, "low", 1.0)
        before = m.estimate("low")
        version = m.version
        self.assertTrue(m.mark(3, "low", TRANSIENT))
        self.assertNotEqual(m.version, version)
        version = m.version
        self.assertFalse(m.mark(3, "low", TRANSIENT))  # repeat: no-op
        self.assertFalse(m.mark(3, "low", TRANSIENT))
        self.assertEqual(m.version, version)
        self.assertEqual(m.estimate("low").value, m.estimate("low").value)
        self.assertEqual(m.valid_count("low"), 7)
        # re-marking back to valid restores the contribution exactly once
        self.assertTrue(m.mark(3, "low", VALID))
        self.assertFalse(m.mark(3, "low", VALID))
        self.assertEqual(m.valid_count("low"), 8)
        self.assertEqual(m.estimate("low").value, before.value)
        # marks for unknown frames are no-ops
        self.assertFalse(m.mark(999, "low", TRANSIENT))


class TestEquivalence(unittest.TestCase):
    def test_chunked_and_out_of_order_consistent(self):
        stream = fixtures.make_stream()
        in_order = NoiseFloorModel(window=16, quantile=0.2)
        in_order.ingest_chunk(stream)

        shuffled = list(stream)
        random.Random(7).shuffle(shuffled)
        chunked = NoiseFloorModel(window=16, quantile=0.2)
        for i in range(0, len(shuffled), 13):
            chunked.ingest_chunk(shuffled[i:i + 13])

        self.assertEqual(in_order.version, chunked.version)
        for scene in fixtures.SCENES:
            for band in fixtures.BANDS:
                a = in_order.estimate(band, scene)
                b = chunked.estimate(band, scene)
                self.assertEqual(a.estimable, b.estimable)
                if a.estimable:
                    self.assertEqual(a.value, b.value)

    def test_save_restore_consistent(self):
        stream = fixtures.make_stream()
        m = NoiseFloorModel(window=16, quantile=0.2)
        m.ingest_chunk(stream[:120])
        m.mark(fixtures.PEAK_FRAME, fixtures.PEAK_BAND, TRANSIENT,
               scene=fixtures.scene_of(fixtures.PEAK_FRAME, 96))
        m.detect("mid", 5.0, frame=50, scene="office")

        restored = NoiseFloorModel.from_state(m.to_state())
        self.assertEqual(restored.version, m.version)
        for scene in fixtures.SCENES:
            for band in fixtures.BANDS:
                a = m.estimate(band, scene)
                b = restored.estimate(band, scene)
                self.assertEqual(a.estimable, b.estimable)
                if a.estimable:
                    self.assertAlmostEqual(a.value, b.value, delta=TOL)
        # both models evolve identically after restore
        m.ingest_chunk(stream[120:])
        restored.ingest_chunk(stream[120:])
        for scene in fixtures.SCENES:
            for band in fixtures.BANDS:
                a = m.estimate(band, scene)
                b = restored.estimate(band, scene)
                self.assertEqual(a.estimable, b.estimable)
                if a.estimable:
                    self.assertEqual(a.value, b.value)


class TestExactQuantileVsOracle(unittest.TestCase):
    def test_exact_quantile_duplicates_and_access_count(self):
        window, q, n_frames, n_ops = 9, 0.3, 60, 300
        rng = random.Random(1234)
        m = NoiseFloorModel(window=window, quantile=q)
        oracle = SortOracle(window, q)
        ops = 0
        for _ in range(n_ops):
            frame = rng.randrange(n_frames)
            if rng.random() < 0.6 or frame not in oracle.samples:
                value = round(rng.uniform(0.5, 2.0), 1)  # force duplicates
                status = rng.choice([VALID, VALID, VALID, TRANSIENT, MISSING])
                sample_value = value if status != MISSING else None
                m.ingest(frame, "low", sample_value, status)
                oracle.ingest(frame, sample_value, status)
            else:
                status = rng.choice([VALID, TRANSIENT, MISSING])
                m.mark(frame, "low", status)
                oracle.mark(frame, status)
            ops += 1
            want = oracle.estimate()
            got = m.estimate("low")
            if want is None:
                self.assertFalse(got.estimable)
            else:
                self.assertTrue(got.estimable)
                self.assertAlmostEqual(got.value, want, delta=TOL)

        # incremental access count: far below a per-frame full sort
        comparisons = m.comparisons("low")
        per_frame_sort = ops * window * math.log2(window)
        self.assertLess(comparisons, per_frame_sort / 4)
        self.assertLess(comparisons, ops * (2 * math.ceil(math.log2(window + 1)) + 2))

    def test_large_window_with_duplicates(self):
        window, n = 4096, 6000
        rng = random.Random(99)
        m = NoiseFloorModel(window=window, quantile=0.1)
        values = []
        for f in range(n):
            v = float(rng.randrange(4))  # heavy duplication
            values.append(v)
            m.ingest(f, "low", v)
        tail = sorted(values[-window:])
        h = (len(tail) - 1) * 0.1
        lo = int(h)
        want = tail[lo] + (tail[lo + 1] - tail[lo]) * (h - lo)
        got = m.estimate("low")
        self.assertTrue(got.estimable)
        self.assertAlmostEqual(got.value, want, delta=TOL)
        bound = n * (2 * math.ceil(math.log2(window + 1)) + 2)
        self.assertLess(m.comparisons("low"), bound)


class TestVersionedDetection(unittest.TestCase):
    def test_threshold_binds_version_and_corrects(self):
        m = NoiseFloorModel(window=6, quantile=0.5)
        for f in range(6):
            m.ingest(f, "low", 1.0)
        det_ok = m.detect("low", 2.0, frame=6, factor=3.0)
        self.assertFalse(det_ok.decision)
        self.assertEqual(det_ok.version, m.version)

        # three extreme peaks enter the window and inflate the baseline
        for f in range(6, 9):
            m.ingest(f, "low", 100.0)
        det_borderline = m.detect("low", 40.0, frame=9, factor=3.0)
        self.assertFalse(det_borderline.decision)  # masked by inflated floor
        stale_version = det_borderline.version

        # late marks retract the peaks; version moves on
        for f in range(6, 9):
            m.mark(f, "low", TRANSIENT)
        self.assertGreater(m.version, stale_version)
        self.assertAlmostEqual(m.estimate("low").value, 1.0, delta=TOL)

        corrections = m.correct_detections()
        self.assertEqual(len(corrections), 2)
        by_id = {c.det_id: c for c in corrections}
        self.assertFalse(by_id[det_ok.det_id].changed)
        self.assertTrue(by_id[det_borderline.det_id].changed)
        self.assertTrue(det_borderline.decision)  # 40 > 3 * 1.0 now
        self.assertEqual(det_borderline.version, m.version)
        # second pass is a no-op: everything is bound to the current version
        self.assertEqual(m.correct_detections(), [])


class TestScenesAndReclaim(unittest.TestCase):
    def test_scene_switch_preserves_baselines(self):
        m = NoiseFloorModel(window=8, quantile=0.5)
        m.set_scene("office")
        for f in range(8):
            m.ingest(f, "low", 1.0)
        office_est = m.estimate("low")

        m.set_scene("street")
        for f in range(8):
            m.ingest(f, "low", 5.0)
        street_est = m.estimate("low")
        self.assertAlmostEqual(street_est.value, 5.0, delta=TOL)

        m.set_scene("office")
        back = m.estimate("low")
        self.assertEqual(back.value, office_est.value)
        self.assertEqual(back.n_valid, office_est.n_valid)
        # office window continues where it left off
        m.ingest(8, "low", 1.0)
        self.assertEqual(m.valid_count("low"), 9)
        self.assertEqual(m.valid_count("low", scene="street"), 8)

    def test_reclaim_bounded_by_query_watermark(self):
        m = NoiseFloorModel(window=4, quantile=0.5)
        qid = m.acquire_query(oldest_frame=10)
        for f in range(20):
            m.ingest(f, "low", 1.0 + f * 0.001)
        m.mark(12, "low", TRANSIENT)  # retained: inside watermark horizon
        dropped = m.reclaim()
        self.assertGreater(dropped, 0)
        self.assertEqual(m.retained_frames("low"), list(range(10, 20)))
        # late mark inside the horizon still works after reclaim
        self.assertTrue(m.mark(15, "low", TRANSIENT))

        m.release_query(qid)
        m.reclaim()
        self.assertEqual(m.retained_frames("low"), [16, 17, 18, 19])
        # reclaimed frames can no longer be marked; the miss is a clean no-op
        self.assertFalse(m.mark(5, "low", TRANSIENT))
        self.assertFalse(m.mark(12, "low", VALID))

    def test_missing_never_enters_window(self):
        m = NoiseFloorModel(window=4, quantile=0.5)
        m.ingest(0, "low", 1.0)
        m.ingest(1, "low", None, MISSING)
        m.ingest(2, "low", 1.0)
        self.assertEqual(m.valid_count("low"), 2)
        self.assertEqual(m.estimate("low").n_valid, 2)
        # a missing frame later reported valid without a value stays out
        self.assertFalse(m.mark(1, "low", VALID))
        self.assertEqual(m.valid_count("low"), 2)


if __name__ == "__main__":
    unittest.main()
