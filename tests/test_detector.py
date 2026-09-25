"""Window RMS, hysteresis, tolerance and multi-track strategy tests."""

import unittest

import fixtures
from silseg import (
    RMS_ABS_TOL,
    RMS_REL_TOL,
    StreamingDetector,
    Track,
    hysteresis_update,
    segment_tracks,
)


class TestWindowRms(unittest.TestCase):
    def test_rms_known_values(self):
        t = Track("t", fixtures.RATE)
        t.add_samples(0, [1.0, -1.0, 1.0, -1.0])
        self.assertAlmostEqual(t.rms_in(0, 4), 1.0)
        self.assertIsNone(t.rms_in(0, 0))  # no data -> None

    def test_rms_ignores_gaps(self):
        t = Track("t", fixtures.RATE)
        t.add_samples(10, [0.5] * 10)
        self.assertIsNone(t.rms_in(0, 10))            # pure gap
        self.assertAlmostEqual(t.rms_in(0, 20), 0.5)  # only real samples count


class TestHysteresis(unittest.TestCase):
    def test_holds_state_between_thresholds(self):
        mid = (fixtures.LOW + fixtures.HIGH) / 2.0
        self.assertTrue(hysteresis_update(mid, True, fixtures.LOW, fixtures.HIGH))
        self.assertFalse(hysteresis_update(mid, False, fixtures.LOW, fixtures.HIGH))

    def test_crosses_only_beyond_band(self):
        self.assertTrue(hysteresis_update(0.4, False, fixtures.LOW, fixtures.HIGH))
        self.assertFalse(hysteresis_update(0.01, True, fixtures.LOW, fixtures.HIGH))

    def test_no_data_keeps_state(self):
        self.assertTrue(hysteresis_update(None, True, fixtures.LOW, fixtures.HIGH))
        self.assertFalse(hysteresis_update(None, False, fixtures.LOW, fixtures.HIGH))

    def test_threshold_equality_is_conservative(self):
        # Declared: within tolerance of a threshold -> no transition.
        self.assertFalse(
            hysteresis_update(fixtures.HIGH, False, fixtures.LOW, fixtures.HIGH))
        self.assertTrue(
            hysteresis_update(fixtures.LOW, True, fixtures.LOW, fixtures.HIGH))

    def test_clearly_outside_tolerance_flips(self):
        eps = fixtures.HIGH * RMS_REL_TOL * 10 + RMS_ABS_TOL * 10
        self.assertTrue(
            hysteresis_update(fixtures.HIGH + eps, False, fixtures.LOW, fixtures.HIGH))
        self.assertFalse(
            hysteresis_update(fixtures.LOW - eps, True, fixtures.LOW, fixtures.HIGH))


class TestStrategies(unittest.TestCase):
    def test_any_vs_all(self):
        params = dict(fixtures.PARAMS)
        any_segs = segment_tracks(fixtures.fixture_multitrack(), **params)
        self.assertEqual([(s.start, s.end) for s in any_segs],
                         [(720, 1680), (2320, 3280)])
        all_segs = segment_tracks(fixtures.fixture_multitrack(),
                                  **dict(params, strategy="all"))
        self.assertEqual(all_segs, [])  # tracks never loud simultaneously

    def test_gap_window_casts_no_vote(self):
        # Track "a" has a gap where track "b" is loud: 'all' must not treat
        # the gap as silence.
        a = Track("a", fixtures.RATE)
        a.add_samples(0, fixtures.silence(800) + fixtures.tone(440, 800))
        b = Track("b", fixtures.RATE)
        b.add_samples(1600, fixtures.tone(330, 800))
        det = StreamingDetector([a, b], fixtures.WINDOW,
                                fixtures.LOW, fixtures.HIGH, strategy="all")
        det.finalize(2400)
        # windows 10-19: only "a" votes (loud); 20-29: only "b" votes (loud)
        self.assertTrue(all(det.loud[10:30]))

    def test_no_track_votes_means_silent(self):
        t = Track("t", fixtures.RATE)
        t.add_samples(1600, fixtures.tone(440, 800))
        det = StreamingDetector([t], fixtures.WINDOW,
                                fixtures.LOW, fixtures.HIGH)
        det.finalize(2400)
        self.assertFalse(any(det.loud[:20]))  # leading gap is silent
        self.assertTrue(all(det.loud[20:30]))


if __name__ == "__main__":
    unittest.main()
