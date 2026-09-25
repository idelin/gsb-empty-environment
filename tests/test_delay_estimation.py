"""Runnable unittest suite for the sliding-window NCC delay estimator.

Run with::

    PYTHONPATH=src python3.11 -m unittest -v tests.test_delay_estimation
"""

from __future__ import annotations

import json
import os
import random
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))

from delayest import DelayEstimator, estimate_delays, slice_windows  # noqa: E402
from delayest.dp import solve_trajectory  # noqa: E402
from delayest.ncc import LagPeak, WindowEvidence, window_evidence  # noqa: E402

try:  # package-style invocation
    from .fixtures import (  # noqa: E402
        FS,
        HOP,
        WINDOW_LEN,
        drifting_pair,
        periodic_pair,
        unique_delay_pair,
    )
except ImportError:  # direct script invocation from tests/
    sys.path.insert(0, os.path.dirname(__file__))
    from fixtures import (  # noqa: E402
        FS,
        HOP,
        WINDOW_LEN,
        drifting_pair,
        periodic_pair,
        unique_delay_pair,
    )


def make_evidence(window_index, lags, ncc=0.95, polarity=1, unique=False):
    peaks = [LagPeak(lag=int(lag), ncc=float(ncc), polarity=polarity) for lag in lags]
    if unique:
        assert len(lags) == 1
    return WindowEvidence(
        window_index=window_index,
        start=window_index * HOP,
        stop=window_index * HOP + WINDOW_LEN,
        peaks=peaks,
        curve={},
        n_pairs=WINDOW_LEN,
        polarity=polarity,
        unique=len(peaks) == 1,
        status="evidence",
    )


def unknown_evidence(window_index, status="silent", reason=""):
    return WindowEvidence(
        window_index=window_index,
        start=window_index * HOP,
        stop=window_index * HOP + WINDOW_LEN,
        peaks=[],
        curve={},
        n_pairs=0,
        polarity=None,
        unique=False,
        status=status,
        reason=reason,
    )


class PeriodicSignalTests(unittest.TestCase):
    def test_periodic_signal_has_tied_equal_peaks_not_unique(self):
        a, b = periodic_pair(n=WINDOW_LEN, period=4, lag=0)
        ev = window_evidence(a, b, 0, 0, WINDOW_LEN, max_lag=12)
        self.assertEqual(ev.status, "evidence")
        self.assertGreaterEqual(len(ev.peaks), 3, "periodic signal must yield repeated peaks")
        self.assertFalse(ev.unique, "multiple equal peaks must not be claimed unique")
        ncc_values = [peak.ncc for peak in ev.peaks]
        self.assertAlmostEqual(max(ncc_values), min(ncc_values), places=9)
        lags = sorted(peak.lag for peak in ev.peaks)
        self.assertEqual(lags, sorted(lags))
        for lag in lags:
            self.assertIn(lag, range(-12, 13))
        # Estimator surface keeps ambiguity visible.
        est = DelayEstimator(fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=12)
        est.add_evidence(ev)
        result = est.solve()
        window = result.windows[0]
        self.assertTrue(window.ambiguous)
        self.assertEqual(window.candidate_lags, lags)
        self.assertIsNotNone(window.uncertainty_samples)
        self.assertGreaterEqual(window.uncertainty_samples, max(lags) - min(lags))


class LowInformationTests(unittest.TestCase):
    def test_silence_returns_unknown(self):
        a = [0.0] * WINDOW_LEN
        b = [0.0] * WINDOW_LEN
        ev = window_evidence(a, b, 0, 0, WINDOW_LEN, max_lag=10)
        self.assertEqual(ev.status, "silent")
        self.assertFalse(ev.known)
        self.assertEqual(ev.peaks, [])

        est = DelayEstimator(fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=10)
        est.add_evidence(ev)
        result = est.solve()
        window = result.windows[0]
        self.assertIsNone(window.lag_samples)
        self.assertIsNone(window.lag_seconds)
        self.assertIsNone(window.uncertainty_samples)

    def test_masked_insufficient_intersection_returns_unknown(self):
        rng = random.Random(11)
        a = [rng.gauss(0, 1) for _ in range(WINDOW_LEN)]
        b = [rng.gauss(0, 1) for _ in range(WINDOW_LEN)]
        # Only 3 samples of b are valid. At lag 0 the intersection is 3
        # samples; non-zero lags make it smaller. Below min_overlap_pairs.
        mask_b = [False] * WINDOW_LEN
        for i in (10, 64, 110):
            mask_b[i] = True
        ev = window_evidence(
            a, b, 2, 0, WINDOW_LEN, max_lag=10, mask_a=[True] * WINDOW_LEN, mask_b=mask_b
        )
        self.assertEqual(ev.status, "insufficient_overlap")
        self.assertFalse(ev.known)
        est = DelayEstimator(
            fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=10, min_overlap_pairs=8
        )
        est.add_evidence(ev)
        window = est.solve().windows[2]
        self.assertIsNone(window.lag_samples)
        self.assertEqual(window.status, "insufficient_overlap")

    def test_constant_channel_is_silent_even_with_overlap(self):
        a = [0.5] * WINDOW_LEN
        b = [(-1.0) ** i for i in range(WINDOW_LEN)]
        ev = window_evidence(a, b, 0, 0, WINDOW_LEN, max_lag=5)
        self.assertEqual(ev.status, "silent")


class FractionalDriftTests(unittest.TestCase):
    D0 = 3.5
    SLOPE = 0.2  # samples per window

    def setUp(self):
        payload = drifting_pair(
            n=900, d0=self.D0, slope=self.SLOPE, seed=42, missing_fraction=0.06
        )
        self.a, self.b, self.mask_a, self.mask_b, self.delay_at = payload
        self.est = DelayEstimator(
            fs=FS,
            window_len=WINDOW_LEN,
            hop=HOP,
            max_lag=12,
            velocities=(-1, 0, 1),
            max_jump=2,
        )
        for idx, start, stop in slice_windows(len(self.a), WINDOW_LEN, HOP):
            self.est.add_window(
                self.a, self.b, idx, start, stop, self.mask_a, self.mask_b
            )

    def test_trajectory_tracks_fractional_delay_and_linear_drift(self):
        result = self.est.solve()
        windows = result.ordered()
        known = [w for w in windows if w.known]
        self.assertGreaterEqual(len(known), int(0.9 * len(windows)))

        errors = []
        for w in known:
            # The local NCC peak measures the mean delay over the window's
            # valid samples; for a per-sample linear drift that is the delay
            # at the mean sample position (start + (window_len - 1) / 2).
            mean_sample = w.start + (WINDOW_LEN - 1) / 2.0
            slope_per_sample = self.SLOPE / HOP
            expected = self.D0 + slope_per_sample * mean_sample
            self.assertIsNotNone(w.uncertainty_samples)
            errors.append(abs(w.lag_samples - expected))
            self.assertLess(
                abs(w.lag_samples - expected),
                0.3,
                f"window {w.window_index}: got {w.lag_samples}, expected {expected}",
            )
        self.assertLess(sum(errors) / len(errors), 0.12)

        # Fit a line over the estimated trajectory; slope must recover drift.
        m = len(known)
        mx = sum(w.window_index for w in known) / m
        my = sum(w.lag_samples for w in known) / m
        cov = sum((w.window_index - mx) * (w.lag_samples - my) for w in known)
        var = sum((w.window_index - mx) ** 2 for w in known)
        fitted_slope = cov / var
        fitted_intercept = my - fitted_slope * mx
        self.assertAlmostEqual(fitted_slope, self.SLOPE, delta=0.08)
        expected_intercept = self.D0 + self.SLOPE * (WINDOW_LEN - 1) / (2 * HOP)
        self.assertAlmostEqual(fitted_intercept, expected_intercept, delta=0.15)

        # Seconds conversion.
        for w in known[:3]:
            self.assertAlmostEqual(w.lag_seconds, w.lag_samples / FS, places=12)

    def test_polarity_flipped_channel(self):
        payload = drifting_pair(n=700, d0=4.0, slope=0.0, seed=7, polarity_b=-1)
        a, b, ma, mb, _ = payload
        result = estimate_delays(
            [a, b], FS, WINDOW_LEN, HOP, max_lag=10, masks=[ma, mb],
            velocities=(0,), max_jump=1,
        )
        known = [w for w in result.ordered() if w.known]
        self.assertTrue(known)
        for w in known:
            self.assertEqual(w.polarity, -1)
            self.assertAlmostEqual(w.lag_samples, 4.0, delta=0.9)


class LateWindowRevisionTests(unittest.TestCase):
    def _periodic_evidence(self, idx, period=3):
        a, b = periodic_pair(n=WINDOW_LEN, period=period, lag=0)
        return window_evidence(a, b, idx, idx * HOP, idx * HOP + WINDOW_LEN, max_lag=10)

    def test_late_window_revises_earlier_path(self):
        est = DelayEstimator(
            fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=10,
            velocities=(0, 1), max_jump=3,
        )
        indices = [0, 1, 2]
        for idx in indices:
            est.add_evidence(self._periodic_evidence(idx))
        before = est.solve()
        initial_lags = {i: before.windows[i].lag_samples for i in indices}
        for i in indices:
            self.assertTrue(before.windows[i].ambiguous)
        # Deterministic tie-break picks the smallest-magnitude tied lag.
        self.assertEqual(initial_lags[0], min(before.windows[0].candidate_lags, key=abs))

        # Window 3 arrives late (only windows 0..2 were available first) and
        # pins the continuous trajectory at lag 3 with unique strong evidence.
        a3, b3 = unique_delay_pair(WINDOW_LEN, lag=3, seed=71)
        late = window_evidence(a3, b3, 3, 3 * HOP, 3 * HOP + WINDOW_LEN, max_lag=10)
        est.add_evidence(late)
        after = est.solve()

        revised = [
            i for i in indices
            if abs(after.windows[i].lag_samples - initial_lags[i]) > 1e-9
        ]
        self.assertTrue(
            revised,
            "late unique evidence must retroactively revise earlier tied choices",
        )
        for i in indices:
            self.assertEqual(after.windows[i].lag_int_samples, 3)
            self.assertAlmostEqual(after.windows[i].lag_samples, 3.0, delta=0.1)
        self.assertLessEqual(after.trajectory_cost, before.trajectory_cost + 0.02)

    def test_late_window_revises_future_path_across_gap(self):
        est = DelayEstimator(
            fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=10,
            velocities=(0, 1), max_jump=3,
        )
        # Windows 0..2 periodic (tied), window 3 is silent/unknown, then
        # windows 4..5 periodic again; all solved before window 3 arrives.
        for idx in (0, 1, 2, 4, 5):
            est.add_evidence(self._periodic_evidence(idx))
        est.add_evidence(unknown_evidence(3, status="silent"))
        before = est.solve()
        initial = {i: before.windows[i].lag_samples for i in (0, 1, 2, 4, 5)}

        # Late strong evidence arrives at window 3 (it was held back) and
        # bridges the gap, anchoring lag 3 on both sides.
        est._evidence.pop(3)
        a3, b3 = unique_delay_pair(WINDOW_LEN, lag=3, seed=83)
        bridge = window_evidence(a3, b3, 3, 3 * HOP, 3 * HOP + WINDOW_LEN, max_lag=10)
        est.add_evidence(bridge)
        after = est.solve()

        changed_after = abs(after.windows[4].lag_samples - initial[4]) > 1e-9
        changed_before = abs(after.windows[1].lag_samples - initial[1]) > 1e-9
        self.assertTrue(changed_after or changed_before)
        for i in (0, 1, 2, 3, 4, 5):
            self.assertEqual(after.windows[i].lag_int_samples, 3)
            self.assertAlmostEqual(after.windows[i].lag_samples, 3.0, delta=0.1)


class AnomalousPeakTests(unittest.TestCase):
    def test_anomalous_peak_cannot_cause_unbounded_jump(self):
        est = DelayEstimator(
            fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=20,
            velocities=(0, 1), max_jump=2, skip_cost=0.4, restart_cost=3.0,
        )
        n_windows = 12
        anomaly_index = 6
        for idx in range(n_windows):
            if idx == anomaly_index:
                # Evidence that is internally strong but wildly inconsistent
                # with the continuous trajectory and the jump bound.
                est.add_evidence(make_evidence(idx, [18], ncc=0.999))
            else:
                a, b = unique_delay_pair(WINDOW_LEN, lag=2, seed=100 + idx)
                est.add_window(a, b, idx, idx * HOP, idx * HOP + WINDOW_LEN)

        result = est.solve()
        lags = {i: result.windows[i].lag_samples for i in range(n_windows)}
        # The anomalous window must be refused (unknown), never followed.
        self.assertIsNone(lags[anomaly_index], "anomalous window should be skipped")
        normal = [i for i in range(n_windows) if i != anomaly_index]
        for i in normal:
            self.assertIsNotNone(lags[i], f"normal window {i} must stay tracked")
            self.assertLessEqual(abs(lags[i] - 2.0), 1.0)
        # The jump bound is respected wherever adjacent windows are known.
        ordered_known = [i for i in range(n_windows) if lags[i] is not None]
        for prev, nxt in zip(ordered_known, ordered_known[1:]):
            if nxt == prev + 1:
                self.assertLessEqual(abs(lags[nxt] - lags[prev]), est.max_jump)

        # Sanity: a naive per-window argmax would have taken the bogus peak.
        naive = est.evidence[anomaly_index].candidate_lags()
        self.assertEqual(naive, [18])

    def test_end_anomaly_is_refused_instead_of_dragging_path(self):
        est = DelayEstimator(
            fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=20,
            velocities=(0,), max_jump=1, skip_cost=0.4, restart_cost=5.0,
        )
        for idx in range(6):
            a, b = unique_delay_pair(WINDOW_LEN, lag=-2, seed=300 + idx)
            est.add_window(a, b, idx, idx * HOP, idx * HOP + WINDOW_LEN)
        est.add_evidence(make_evidence(6, [-17], ncc=0.99))
        result = est.solve()
        self.assertIsNone(result.windows[6].lag_samples)
        for idx in range(6):
            self.assertAlmostEqual(result.windows[idx].lag_samples, -2.0, delta=1.0)


def brute_force_reference(evidence, velocities=(-1, 0, 1), max_jump=3,
                          skip_cost=2.0, restart_cost=3.0, weight_corr=10.0):
    """Independent exhaustive enumeration over finite lag/velocity choices.

    Mirrors solve_trajectory exactly (including carry/gap semantics) but
    performs complete recursion without Viterbi pruning; tests only.
    """
    evidence = sorted(evidence, key=lambda ev: ev.window_index)
    vel_list = list(velocities)
    n = len(evidence)
    options = []
    for ev in evidence:
        options.append(list({p.lag: p.ncc for p in ev.peaks}.items()) if ev.peaks else None)

    best_cost = float("inf")
    best_path = None

    def path_rank(path):
        rank = []
        for item in path:
            if item is None:
                rank.append((1, 0, 0, 0, 0))
            else:
                lag, vel = item
                rank.append((0, abs(lag), abs(vel), lag, vel))
        return tuple(rank)

    def recurse(i, state, cost, path):
        nonlocal best_cost, best_path
        if cost > best_cost + 1e-12:
            return
        if i == n:
            if (
                best_path is None
                or cost < best_cost - 1e-12
                or (abs(cost - best_cost) <= 1e-12 and path_rank(path) < path_rank(best_path))
            ):
                best_cost, best_path = cost, list(path)
            return

        idx = evidence[i].window_index
        if options[i] is None:
            if state[0] == "landed":
                _, last_idx, lag, vel = state
                nxt = ("carry", last_idx, lag, vel, 1)
            elif state[0] == "carry":
                _, last_idx, lag, vel, gap = state
                nxt = ("carry", last_idx, lag, vel, gap + 1)
            else:
                nxt = state
            recurse(i + 1, nxt, cost, path + [None])
            return

        # Refuse evidence.
        if state[0] == "landed":
            _, last_idx, lag, vel = state
            recurse(i + 1, ("carry", last_idx, lag, vel, 0), cost + skip_cost, path + [None])
            recurse(i + 1, "gap", cost + skip_cost, path + [None])
        elif state[0] == "carry":
            _, last_idx, lag, vel, gap = state
            recurse(i + 1, ("carry", last_idx, lag, vel, gap), cost + skip_cost, path + [None])
            recurse(i + 1, "gap", cost + skip_cost, path + [None])
        else:
            recurse(i + 1, "gap", cost + skip_cost, path + [None])

        # Land.
        for lag, ncc in options[i]:
            for vel in vel_list:
                if state == "start":
                    extra = weight_corr * (1.0 - ncc)
                    nxt = ("landed", idx, lag, vel)
                elif state == "gap":
                    extra = restart_cost + weight_corr * (1.0 - ncc)
                    nxt = ("landed", idx, lag, vel)
                elif state[0] == "landed":
                    _, last_idx, plag, pvel = state
                    span = idx - last_idx
                    jump = lag - plag
                    if abs(jump) > max_jump * span or jump != vel * span:
                        continue
                    extra = abs(vel - pvel) ** 2 + weight_corr * (1.0 - ncc)
                    nxt = ("landed", idx, lag, vel)
                else:
                    _, last_idx, plag, pvel, _gap = state
                    span = idx - last_idx
                    jump = lag - plag
                    if abs(jump) > max_jump * span or jump != vel * span:
                        continue
                    extra = abs(vel - pvel) ** 2 + weight_corr * (1.0 - ncc)
                    nxt = ("landed", idx, lag, vel)
                recurse(i + 1, nxt, cost + extra, path + [(lag, vel)])

    recurse(0, "start", 0.0, [])
    return best_cost, best_path


def evaluate_path_cost(evidence, path, velocities=(-1, 0, 1), max_jump=3,
                          skip_cost=2.0, restart_cost=3.0, weight_corr=10.0):
    """Forward simulation of the DP cost semantics for one explicit path."""
    evidence = sorted(evidence, key=lambda ev: ev.window_index)
    ncc_at = {ev.window_index: {p.lag: p.ncc for p in ev.peaks} for ev in evidence}
    is_evidence = {ev.window_index: ev.status == "evidence" for ev in evidence}

    cost = 0.0
    state = "start"  # start | gap | landed(idx,lag,vel) | carry(idx,lag,vel,gap)
    for i, choice in enumerate(path):
        idx = evidence[i].window_index
        if choice is None:
            if is_evidence[idx]:
                cost += skip_cost
                if state == "start" or state == "gap":
                    state = "gap"
                elif state[0] == "landed":
                    _, last_idx, lag, vel = state
                    state = ("carry", last_idx, lag, vel, 0)
            else:
                if state[0] == "landed":
                    _, last_idx, lag, vel = state
                    state = ("carry", last_idx, lag, vel, 1)
                elif state[0] == "carry":
                    _, last_idx, lag, vel, gap = state
                    state = ("carry", last_idx, lag, vel, gap + 1)
            continue

        lag, vel = choice
        if lag not in ncc_at[idx]:
            return float("inf")
        emission = weight_corr * (1.0 - ncc_at[idx][lag])
        if state == "start":
            cost += emission
        elif state == "gap":
            cost += restart_cost + emission
        elif state[0] == "landed":
            _, last_idx, plag, pvel = state
            span = idx - last_idx
            jump = lag - plag
            if abs(jump) > max_jump * span or jump != vel * span:
                return float("inf")
            cost += abs(vel - pvel) ** 2 + emission
        else:
            _, last_idx, plag, pvel, _gap = state
            span = idx - last_idx
            jump = lag - plag
            if abs(jump) > max_jump * span or jump != vel * span:
                return float("inf")
            cost += abs(vel - pvel) ** 2 + emission
        state = ("landed", idx, lag, vel)
    return cost


class ExhaustiveReferenceTests(unittest.TestCase):
    def test_matches_brute_force_on_random_evidence(self):
        rng = random.Random(2024)
        for trial in range(40):
            n = rng.randint(4, 8)
            evidence = []
            base = rng.randint(-3, 3)
            for idx in range(n):
                kind = rng.random()
                if kind < 0.2:
                    evidence.append(unknown_evidence(idx, status="silent"))
                    continue
                n_cand = rng.randint(1, 3)
                lags = sorted({base + rng.randint(-2, 2) for _ in range(n_cand)})
                evidence.append(make_evidence(idx, lags, ncc=rng.uniform(0.5, 0.99)))
            solution = solve_trajectory(evidence, velocities=(-1, 0, 1), max_jump=3)
            ref_cost, ref_path = brute_force_reference(evidence)
            self.assertAlmostEqual(solution.cost, ref_cost, places=9)
            dp_path = [
                (step.lag_int, int(step.velocity)) if step.active else None
                for step in solution.steps
            ]
            # Both the DP path and the enumerated path must attain the same
            # (optimal) cost. Ties may select different optimal trajectories.
            dp_cost = evaluate_path_cost(evidence, dp_path)
            ref_eval = evaluate_path_cost(evidence, ref_path)
            self.assertAlmostEqual(dp_cost, ref_cost, places=9)
            self.assertAlmostEqual(ref_eval, ref_cost, places=9)

    def test_matches_brute_force_on_drift_fixture(self):
        payload = drifting_pair(n=512, d0=2.0, slope=0.15, seed=99, missing_fraction=0.05)
        a, b, ma, mb, _ = payload
        est = DelayEstimator(
            fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=8,
            velocities=(0, 1), max_jump=2,
        )
        est.add_arrays([a, b], [ma, mb])
        evidence = [est.evidence[k] for k in sorted(est.evidence)]
        solution = solve_trajectory(evidence, velocities=(0, 1), max_jump=2)
        ref_cost, _ = brute_force_reference(evidence, velocities=(0, 1), max_jump=2)
        self.assertAlmostEqual(solution.cost, ref_cost, places=9)


class CheckpointTests(unittest.TestCase):
    def test_resume_matches_continuous_run_and_allows_more_windows(self):
        payload = drifting_pair(n=640, d0=2.5, slope=0.1, seed=123, missing_fraction=0.05)
        a, b, ma, mb, _ = payload
        windows = slice_windows(len(a), WINDOW_LEN, HOP)
        first_batch = [w for w in windows if w[0] < 5]
        rest = [w for w in windows if w[0] >= 5]

        # Continuous reference: every window available from the start.
        continuous = DelayEstimator(
            fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=10,
            velocities=(0, 1), max_jump=2,
        )
        for idx, start, stop in windows:
            continuous.add_window(a, b, idx, start, stop, ma, mb)
        expected = continuous.solve()

        # Interrupted run: 5 windows, checkpoint to disk, resume, add rest.
        interrupted = DelayEstimator(
            fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=10,
            velocities=(0, 1), max_jump=2,
        )
        for idx, start, stop in first_batch:
            interrupted.add_window(a, b, idx, start, stop, ma, mb)
        partial = interrupted.solve()
        self.assertTrue(any(w.known for w in partial.ordered()))

        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "checkpoint.json")
            interrupted.save_checkpoint(path)
            with open(path, encoding="utf-8") as handle:
                payload_json = json.load(handle)
            self.assertEqual(len(payload_json["evidence"]), len(first_batch))
            resumed = DelayEstimator.load_checkpoint(path)

        for idx, start, stop in rest:
            resumed.add_window(a, b, idx, start, stop, ma, mb)
        result = resumed.solve()

        self.assertAlmostEqual(result.trajectory_cost, expected.trajectory_cost, places=12)
        self.assertEqual(set(result.windows), set(expected.windows))
        for idx in expected.windows:
            got = result.windows[idx]
            want = expected.windows[idx]
            self.assertEqual(got.lag_samples, want.lag_samples)
            self.assertEqual(got.drift_samples_per_window, want.drift_samples_per_window)
            self.assertEqual(got.status, want.status)
            self.assertEqual(got.polarity, want.polarity)

    def test_checkpoint_roundtrip_preserves_evidence(self):
        est = DelayEstimator(fs=FS, window_len=WINDOW_LEN, hop=HOP, max_lag=9)
        a, b = unique_delay_pair(WINDOW_LEN, lag=3, seed=5)
        est.add_window(a, b, 0, 0, WINDOW_LEN)
        data = est.to_checkpoint()
        restored = DelayEstimator.from_checkpoint(data)
        before = est.solve().windows[0]
        after = restored.solve().windows[0]
        self.assertEqual(before.lag_samples, after.lag_samples)
        self.assertEqual(restored.max_lag, 9)


class MultiChannelTests(unittest.TestCase):
    def test_three_channels_selected_pair(self):
        payload = drifting_pair(n=512, d0=3.0, slope=0.0, seed=55, missing_fraction=0.04)
        a, b, ma, mb, _ = payload
        rng = random.Random(9)
        c = [v + rng.gauss(0, 0.02) for v in b]
        mc = list(mb)
        signals = [a, b, c]
        masks = [ma, mb, mc]
        result = estimate_delays(
            signals, FS, WINDOW_LEN, HOP, max_lag=10, masks=masks,
            pair=(0, 2), velocities=(0,), max_jump=1,
        )
        self.assertEqual(result.pair, (0, 2))
        known = [w for w in result.ordered() if w.known]
        self.assertTrue(known)
        for w in known:
            self.assertAlmostEqual(w.lag_samples, 3.0, delta=0.9)


class DeclarationTests(unittest.TestCase):
    def test_tolerances_and_units_are_declared(self):
        from delayest.tolerances import (
            DEFAULT_LAG_UNCERTAINTY_SAMPLES,
            GRID_HALF_BIN_SAMPLES,
            LAG_UNIT_SAMPLES,
            MIN_OVERLAP_PAIRS,
            MIN_PEAK_NCC,
        )
        self.assertEqual(LAG_UNIT_SAMPLES, 1.0)
        self.assertEqual(GRID_HALF_BIN_SAMPLES, 0.5)
        self.assertEqual(MIN_OVERLAP_PAIRS, 8)
        self.assertTrue(0.0 < MIN_PEAK_NCC < 1.0)
        self.assertGreater(DEFAULT_LAG_UNCERTAINTY_SAMPLES, 0.0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
