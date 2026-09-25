"""Runnable unittest suite for the sliding-window NCC delay estimator.

Ground truth (delays, drift, sample rate) and numeric tolerances are declared
in fixtures.py; no audio devices are involved.
"""

import math
import unittest

import fixtures as F
from delay_estimation import (
    Candidate,
    DelayEstimator,
    TrackerParams,
    WindowObservation,
    exhaustive_track,
    extract_candidates,
    viterbi_track,
)

PARAMS = TrackerParams(step_penalty=0.10, max_jump=2.0, null_penalty=0.5)


def make_obs(index, start, delay_score_pairs, informative=True):
    cands = [Candidate(d, s, 1, 1.0) for d, s in delay_score_pairs]
    cands.sort(key=lambda c: c.score, reverse=True)
    return WindowObservation(index, start, cands, F.WINDOW, informative)


def observe_all(est, x, y, starts, mask_x=None, mask_y=None):
    for s in starts:
        est.observe(x, y, s, F.WINDOW, F.MAX_LAG, mask_x=mask_x, mask_y=mask_y,
                    min_overlap=F.MIN_OVERLAP, min_score=F.MIN_SCORE)


class PeriodicAmbiguityTest(unittest.TestCase):
    """A periodic signal yields several equal-height NCC peaks; the estimator
    must keep them all as candidates and must not claim a unique delay."""

    def test_periodic_signal_reports_multiple_equal_peaks(self):
        period = 32
        x = F.make_sine(F.WINDOW, period)
        y = F.make_sine(F.WINDOW, period)  # true delay 0, aliases at +-period
        est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        obs = est.observe(x, y, 0, F.WINDOW, F.MAX_LAG,
                          min_overlap=F.MIN_OVERLAP, min_score=F.MIN_SCORE)

        top = obs.candidates[0].score
        tied = [c for c in obs.candidates if abs(c.score - top) <= F.TIE_SCORE_TOL]
        # Peaks at lags -64, -32, 0, +32, +64 all reach |NCC| ~= 1.
        self.assertGreaterEqual(len(tied), 3)
        self.assertAlmostEqual(top, 1.0, places=6)
        tied_delays = sorted(c.delay for c in tied)
        for expected in (-2 * period, -period, 0.0, period, 2 * period):
            self.assertTrue(
                any(abs(d - expected) <= F.INT_DELAY_TOL for d in tied_delays),
                f"missing tied peak near {expected}: {tied_delays}")

        estimate = est.estimates()[0]
        self.assertFalse(estimate.unique, "tied peaks must not claim uniqueness")
        self.assertIsNotNone(estimate.uncertainty)
        # Uncertainty is half the distance to the nearest tied competitor
        # (the std of an equally-weighted two-hypothesis mixture); it must
        # dwarf the nominal sub-sample interpolation accuracy.
        self.assertGreater(estimate.uncertainty, 4 * F.INT_DELAY_TOL)


class LowInformationTest(unittest.TestCase):
    """Silence and insufficient overlap must come back as unknown."""

    def test_silence_returns_unknown(self):
        n = F.WINDOW
        for x, y in (([0.0] * n, [0.0] * n),
                     (F.make_white_noise(n), [0.0] * n),
                     ([0.0] * n, F.make_white_noise(n))):
            est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
            est.observe(x, y, 0, F.WINDOW, F.MAX_LAG,
                        min_overlap=F.MIN_OVERLAP, min_score=F.MIN_SCORE)
            estimate = est.estimates()[0]
            self.assertIsNone(estimate.delay)
            self.assertIsNone(estimate.uncertainty)
            self.assertEqual(estimate.evidence, 0.0)
            self.assertFalse(estimate.unique)

    def test_insufficient_overlap_returns_unknown(self):
        n = 1000
        x = F.make_white_noise(n, seed=1)
        y = F.make_white_noise(n, seed=2)
        # x valid only in [0, 40), y valid only in [960, 1000): no lag in
        # [-MAX_LAG, MAX_LAG] can align the valid regions.
        mask_x = F.mask_with_gap(n, 40, n)
        mask_y = F.mask_with_gap(n, 0, 960)
        est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        obs = est.observe(x, y, 0, F.WINDOW, F.MAX_LAG, mask_x=mask_x,
                          mask_y=mask_y, min_overlap=F.MIN_OVERLAP,
                          min_score=F.MIN_SCORE)
        self.assertLess(obs.n_overlap, F.MIN_OVERLAP)
        self.assertFalse(obs.informative)
        self.assertIsNone(est.estimates()[0].delay)

    def test_missing_data_mask_windows(self):
        static = lambda t: 5.0  # noqa: E731
        x, y = F.fractional_delay_pair(delay_fn=static)
        mask_y = F.mask_with_gap(len(y), 600, 1200)

        # Window fully inside the gap -> unknown.
        est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        est.observe(x, y, 640, F.WINDOW, F.MAX_LAG, mask_y=mask_y,
                    min_overlap=F.MIN_OVERLAP, min_score=F.MIN_SCORE)
        self.assertIsNone(est.estimates()[0].delay)

        # Window outside the gap -> nominal estimate.
        est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        est.observe(x, y, 0, F.WINDOW, F.MAX_LAG, mask_y=mask_y,
                    min_overlap=F.MIN_OVERLAP, min_score=F.MIN_SCORE)
        self.assertAlmostEqual(est.estimates()[0].delay, 5.0,
                               delta=F.INT_DELAY_TOL)

        # Window partially overlapping the gap -> still enough valid pairs.
        est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        est.observe(x, y, 100, F.WINDOW, F.MAX_LAG, mask_y=mask_y,
                    min_overlap=F.MIN_OVERLAP, min_score=F.MIN_SCORE)
        self.assertAlmostEqual(est.estimates()[0].delay, 5.0,
                               delta=F.INT_DELAY_TOL)


class DriftTrajectoryTest(unittest.TestCase):
    """Synthetic fractional delay + linear drift, two channels."""

    def test_fractional_delay_with_drift_trajectory(self):
        x, y = F.fractional_delay_pair()
        starts = F.window_starts(F.N_SAMPLES)
        est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        observe_all(est, x, y, starts)
        estimates = est.estimates()
        self.assertEqual(len(estimates), len(starts))

        errors = []
        for estimate in estimates:
            self.assertIsNotNone(estimate.delay, "all windows should be known")
            truth = F.true_delay(estimate.start + F.WINDOW // 2)
            errors.append(abs(estimate.delay - truth))
            self.assertLessEqual(errors[-1], F.TRAJ_TOL,
                                 f"window {estimate.index}: est "
                                 f"{estimate.delay:.3f} vs truth {truth:.3f}")
            self.assertEqual(estimate.polarity, 1)
            self.assertGreater(estimate.evidence, 0.5)
        # The trajectory must actually follow the drift (not a constant fit).
        self.assertGreater(estimates[-1].delay - estimates[0].delay, 5.0)
        print(f"\n[drift] windows={len(estimates)} "
              f"max_err={max(errors):.3f} mean_err="
              f"{sum(errors) / len(errors):.3f} samples "
              f"(tol={F.TRAJ_TOL})")

    def test_polarity_inversion_recovers_delay(self):
        static = lambda t: 5.0  # noqa: E731
        x, y = F.fractional_delay_pair(delay_fn=static, polarity=-1)
        est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        observe_all(est, x, y, F.window_starts(2000))
        for estimate in est.estimates():
            self.assertEqual(estimate.polarity, -1)
            self.assertAlmostEqual(estimate.delay, 5.0, delta=F.INT_DELAY_TOL)

    def test_late_revision_matches_continuous_run(self):
        x, y = F.fractional_delay_pair()
        starts = F.window_starts(F.N_SAMPLES)
        late_index = 10

        est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        for k, s in enumerate(starts):
            if k == late_index:
                # Window not yet arrived: recorded as uninformative.
                est.add_observation(WindowObservation(k, s, [], 0, False))
            else:
                est.observe(x, y, s, F.WINDOW, F.MAX_LAG,
                            min_overlap=F.MIN_OVERLAP, min_score=F.MIN_SCORE)
        before = est.estimates()
        self.assertIsNone(before[late_index].delay)

        # Late-arriving window revises the observation in place.
        cands, n_overlap, informative = extract_candidates(
            x, y, starts[late_index], F.WINDOW, F.MAX_LAG,
            min_overlap=F.MIN_OVERLAP, min_score=F.MIN_SCORE)
        est.revise_observation(late_index, WindowObservation(
            late_index, starts[late_index], cands, n_overlap, informative))
        after = est.estimates()
        self.assertIsNotNone(after[late_index].delay)

        # The revised trajectory must equal a fresh continuous run that had
        # all data from the start.
        fresh = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        observe_all(fresh, x, y, starts)
        reference = fresh.estimates()
        for got, want in zip(after, reference):
            self.assertEqual(got.delay, want.delay)


class RobustnessTest(unittest.TestCase):
    """Anomalous peaks must not cause unbounded jumps in the trajectory."""

    def test_outlier_peak_does_not_cause_unbounded_jump(self):
        n_windows = 12
        est = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        for k in range(n_windows):
            cands = [(3.0, 0.90)]
            if k in (5, 6):
                cands.append((30.0, 0.99))   # strong but unreachable outlier
            if k in (8, 9):
                cands.append((4.5, 0.99))    # reachable but not worth the jump
            est.add_observation(make_obs(k, k * F.HOP, cands))
        estimates = est.estimates()
        delays = [e.delay for e in estimates]
        self.assertTrue(all(d is not None for d in delays))
        for prev, curr in zip(delays, delays[1:]):
            self.assertLessEqual(abs(curr - prev), PARAMS.max_jump + 1e-9)
        for d in delays:
            self.assertAlmostEqual(d, 3.0, places=9)


class RevisionTest(unittest.TestCase):
    """A late revision must be able to retroactively change later windows."""

    PARAMS = TrackerParams(step_penalty=0.20, max_jump=4.0, null_penalty=0.5)

    def build(self, revised):
        est = DelayEstimator(F.SAMPLE_RATE, self.PARAMS)
        for k in range(10):
            if k < 5:
                cands = [(4.0, 0.95)]
            elif k == 5:
                cands = [(4.0, 0.90)] if revised else [(1.0, 0.90)]
            else:
                cands = [(1.0, 0.90), (4.0, 0.90)]  # locally ambiguous
            est.add_observation(make_obs(k, k * F.HOP, cands))
        return est

    def test_revision_retroactively_updates_path(self):
        est = self.build(revised=False)
        before = [e.delay for e in est.estimates()]
        # Continuity drags the ambiguous tail onto the wrong branch.
        self.assertEqual(before[5:], [1.0] * 5)

        # Late data corrects window 5; the DP re-runs and the tail flips back.
        est.revise_observation(5, make_obs(5, 5 * F.HOP, [(4.0, 0.90)]))
        after = [e.delay for e in est.estimates()]
        self.assertEqual(after, [4.0] * 10)
        self.assertNotEqual(before[6:], after[6:],
                            "revision should retroactively change later windows")


class DpReferenceTest(unittest.TestCase):
    """The Viterbi DP must agree with exhaustive enumeration of all
    finite candidate combinations."""

    PARAMS = TrackerParams(step_penalty=0.15, max_jump=2.0, null_penalty=0.4)

    def test_dp_matches_exhaustive_reference(self):
        observations = [
            make_obs(0, 0, [(1.0, 0.80), (2.0, 0.70)]),
            make_obs(1, F.HOP, [(1.2, 0.90), (2.5, 0.60)]),
            make_obs(2, 2 * F.HOP, [], informative=False),  # null state only
            make_obs(3, 3 * F.HOP, [(1.0, 0.70), (3.0, 0.95)]),
            make_obs(4, 4 * F.HOP, [(1.1, 0.85)]),
        ]
        dp_states, dp_obj = viterbi_track(observations, self.PARAMS)
        ref_states, ref_obj = exhaustive_track(observations, self.PARAMS)
        self.assertAlmostEqual(dp_obj, ref_obj, places=12)
        self.assertEqual(dp_states, ref_states)


class CheckpointTest(unittest.TestCase):
    """Checkpoint/restore must be bit-identical to a continuous run."""

    def test_checkpoint_restore_matches_continuous_run(self):
        x, y = F.fractional_delay_pair()
        starts = F.window_starts(F.N_SAMPLES)
        split = 8

        continuous = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        observe_all(continuous, x, y, starts)

        head = DelayEstimator(F.SAMPLE_RATE, PARAMS)
        observe_all(head, x, y, starts[:split])
        blob = head.checkpoint()

        resumed = DelayEstimator.restore(blob)
        observe_all(resumed, x, y, starts[split:])

        continuous_delays = [e.delay for e in continuous.estimates()]
        resumed_delays = [e.delay for e in resumed.estimates()]
        self.assertEqual(continuous_delays, resumed_delays)

        # A second restore from the same blob converges to the same state.
        again = DelayEstimator.restore(blob)
        observe_all(again, x, y, starts[split:])
        self.assertEqual(continuous_delays,
                         [e.delay for e in again.estimates()])

        # Revisions applied after restore behave exactly as in the
        # continuous instance.
        late = starts[5]
        cands, n_overlap, informative = extract_candidates(
            x, y, late, F.WINDOW, F.MAX_LAG,
            min_overlap=F.MIN_OVERLAP, min_score=F.MIN_SCORE)
        for target in (continuous, resumed):
            target.revise_observation(5, WindowObservation(
                5, late, cands, n_overlap, informative))
        self.assertEqual([e.delay for e in continuous.estimates()],
                         [e.delay for e in resumed.estimates()])


if __name__ == "__main__":
    unittest.main()
