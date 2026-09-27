"""Tests for poisson_process.

Each test is labeled EXACT (deterministic, mathematically exact conclusion)
or EMPIRICAL (statistical estimate compared against the analytic expected
count, i.e. the exact integral of the intensity).
"""

import math
import unittest

from poisson_process import (
    ExponentialStream,
    LinearPiece,
    PoissonSimulator,
    expected_count_homogeneous,
    expected_count_piecewise_constant,
    expected_count_piecewise_linear,
)


class ScriptedStream:
    """Caller-injected deterministic Exp(1) source for exact tests."""

    def __init__(self, values, seed="scripted"):
        self._values = iter(values)
        self.seed = seed
        self.draws = 0

    def next(self):
        self.draws += 1
        return next(self._values)


class TestExactInversion(unittest.TestCase):
    """EXACT: stable inverse of the cumulative hazard, hand-verified values."""

    def test_linear_increasing_from_zero(self):
        # lambda(t) = t on [0, 4]; Lambda(t) = t^2 / 2, total hazard 8.
        # Draw 2.0 -> event at t = 2.0 exactly (2^2 / 2 = 2).
        # Draw 6.0 exhausts the remaining budget 8 - 2 = 6 exactly at t_end,
        # so no event is emitted at the (half-open) right boundary.
        sim = PoissonSimulator(ScriptedStream([2.0, 6.0, 100.0]))
        events = sim.generate_piecewise_linear([(0.0, 0.0), (4.0, 4.0)])
        self.assertEqual(events, [2.0])
        self.assertEqual(sim.stream.draws, 2)

    def test_linear_decreasing_to_zero(self):
        # lambda(t) = 2 - t on [0, 2]; Lambda(t) = 2t - t^2/2.
        # Draw 1.5 -> 2t - t^2/2 = 1.5 -> t = 1.0 (stable root, slope < 0).
        sim = PoissonSimulator(ScriptedStream([1.5, 10.0]))
        events = sim.generate_piecewise_linear([(0.0, 2.0), (2.0, 0.0)])
        self.assertEqual(events, [1.0])

    def test_invert_matches_numeric_integral(self):
        # EXACT: for a random interior hazard h, Lambda(start + invert(h)) == h.
        piece = LinearPiece(1.0, 5.0, 0.5, 3.5)
        for h in (1e-9, 0.3, 3.7, 7.999):
            t = piece.start + piece.invert(h)
            # trapezoid integral of the linear intensity up to t
            lam_t = 0.5 + (3.5 - 0.5) * (t - 1.0) / 4.0
            self.assertAlmostEqual(0.5 * (0.5 + lam_t) * (t - 1.0), h, places=12)


class TestExactIntervalHandling(unittest.TestCase):
    """EXACT: zero-intensity intervals, boundaries, no per-tick approximation."""

    def test_zero_intensity_long_interval_skipped(self):
        segments = [(0.0, 1.0, 1.0), (1.0, 1001.0, 0.0), (1001.0, 1002.0, 1.0)]
        sim = PoissonSimulator(seed=11)
        events = sim.generate_piecewise_constant(segments)
        self.assertTrue(events)
        self.assertTrue(all(e < 1.0 or e >= 1001.0 for e in events))
        # The zero-intensity gap consumes no random draws.
        self.assertLessEqual(sim.stream.draws, len(events) + 1)

    def test_zero_rate_homogeneous_produces_no_events(self):
        sim = PoissonSimulator(seed=3)
        self.assertEqual(sim.generate_homogeneous(0.0, 0.0, 100.0), [])
        self.assertEqual(sim.stream.draws, 0)

    def test_boundary_event_not_duplicated(self):
        # Two segments with hazard 2.0 each; first draw exhausts segment 1
        # exactly, so the event lands once at t = 1.0 (start of segment 2).
        sim = PoissonSimulator(ScriptedStream([2.0, 0.5, 100.0]))
        events = sim.generate_piecewise_constant([(0.0, 1.0, 2.0), (1.0, 2.0, 2.0)])
        self.assertEqual(events, [1.0, 1.25])
        self.assertEqual(events.count(1.0), 1)

    def test_boundary_consistent_with_oneshot(self):
        # The same scripted draws through one homogeneous interval give the
        # same boundary event at t = 1.0, exactly once.
        sim = PoissonSimulator(ScriptedStream([2.0, 0.5, 100.0]))
        events = sim.generate_homogeneous(2.0, 0.0, 2.0)
        self.assertEqual(events, [1.0, 1.25])


class TestBlockConsistencyAndRecovery(unittest.TestCase):
    """EXACT: same Exp(1) sequence => block generation == whole generation."""

    KNOTS = [(0.0, 0.5), (3.0, 2.0), (6.0, 0.0), (9.0, 3.0)]

    def assertEventsConsistent(self, whole, blocked):
        # Event counts are exactly equal; event times agree to 1e-9 because
        # a mid-piece split evaluates equivalent hazard arithmetic in a
        # different floating-point order (differences observed: ~1e-15).
        self.assertEqual(len(whole), len(blocked))
        for w, b in zip(whole, blocked):
            self.assertAlmostEqual(w, b, delta=1e-9)

    def test_piecewise_linear_block_equals_whole(self):
        whole = PoissonSimulator(seed=123).generate_piecewise_linear(self.KNOTS)
        sim = PoissonSimulator(seed=123)
        blocked = sim.generate_piecewise_linear(self.KNOTS, 0.0, 4.0)
        blocked += sim.generate_piecewise_linear(self.KNOTS, 4.0, 9.0)
        self.assertEventsConsistent(whole, blocked)
        self.assertTrue(whole)  # non-trivial sample

    def test_homogeneous_block_equals_whole(self):
        whole = PoissonSimulator(seed=5).generate_homogeneous(1.5, 0.0, 10.0)
        sim = PoissonSimulator(seed=5)
        blocked = sim.generate_homogeneous(1.5, 0.0, 3.0)
        blocked += sim.generate_homogeneous(1.5, 3.0, 10.0)
        self.assertEventsConsistent(whole, blocked)

    def test_checkpoint_recovery(self):
        knots_a = [(0.0, 0.5), (4.0, 2.5)]
        knots_b = [(4.0, 2.5), (9.0, 1.0)]
        sim = PoissonSimulator(seed=99)
        events_a = sim.generate_piecewise_linear(knots_a)
        checkpoint = sim.checkpoint()
        events_b = sim.generate_piecewise_linear(knots_b)
        # Recover from the checkpoint and replay the second block.
        recovered = PoissonSimulator.recover(checkpoint)
        events_b2 = recovered.generate_piecewise_linear(knots_b)
        self.assertEqual(events_b, events_b2)
        # And the joined run equals one-shot generation with the same seed.
        whole = PoissonSimulator(seed=99).generate_piecewise_linear(
            [(0.0, 0.5), (4.0, 2.5), (9.0, 1.0)]
        )
        self.assertEqual(events_a + events_b, whole)

    def test_intensity_version_switch_does_not_resample(self):
        # Intensity version switches at t = 5: existing events are kept.
        sim = PoissonSimulator(seed=7)
        events_v1 = sim.generate_homogeneous(1.0, 0.0, 5.0)
        events_v2 = sim.generate_homogeneous(4.0, 5.0, 10.0)
        # Re-running from scratch with the piecewise version must reproduce
        # the pre-switch events exactly (no resampling) and the same tail.
        fresh = PoissonSimulator(seed=7).generate_piecewise_constant(
            [(0.0, 5.0, 1.0), (5.0, 10.0, 4.0)]
        )
        self.assertEqual(events_v1 + events_v2, fresh)
        self.assertTrue(all(e < 5.0 for e in events_v1))
        self.assertTrue(all(e >= 5.0 for e in events_v2))

    def test_replay_same_seed(self):
        a = PoissonSimulator(seed=42).generate_piecewise_linear(self.KNOTS)
        b = PoissonSimulator(seed=42).generate_piecewise_linear(self.KNOTS)
        self.assertEqual(a, b)


class TestValidationAndMetadata(unittest.TestCase):
    """EXACT: negative intensity rejected; seed and sample size recorded."""

    def test_negative_intensity_rejected(self):
        with self.assertRaises(ValueError):
            PoissonSimulator(seed=1).generate_homogeneous(-0.1, 0.0, 1.0)
        with self.assertRaises(ValueError):
            PoissonSimulator(seed=1).generate_piecewise_constant([(0.0, 1.0, -2.0)])
        with self.assertRaises(ValueError):
            PoissonSimulator(seed=1).generate_piecewise_linear([(0.0, 1.0), (1.0, -0.5)])
        with self.assertRaises(ValueError):
            LinearPiece(0.0, 1.0, 0.0, -1e-12)
        with self.assertRaises(ValueError):
            expected_count_homogeneous(-1.0, 0.0, 1.0)

    def test_seed_and_sample_size_recorded(self):
        sim = PoissonSimulator(seed=20260927)
        events = sim.generate_homogeneous(2.0, 0.0, 5.0)
        record = sim.history[-1]
        self.assertEqual(record.seed, 20260927)
        self.assertEqual(record.n_events, len(events))
        self.assertEqual(record.draws_used, sim.stream.draws)
        self.assertGreaterEqual(record.draws_used, record.n_events)

    def test_analytic_expected_counts(self):
        # EXACT: analytic integrals of the intensity (reference values).
        self.assertEqual(expected_count_homogeneous(2.0, 0.0, 5.0), 10.0)
        segments = [(0.0, 2.0, 1.5), (2.0, 10.0, 0.0), (10.0, 12.0, 3.0)]
        self.assertEqual(expected_count_piecewise_constant(segments), 9.0)
        knots = [(0.0, 0.0), (4.0, 4.0), (8.0, 2.0)]
        self.assertEqual(expected_count_piecewise_linear(knots), 20.0)
        # Clipped to [2, 6]: trapezoids (2+4)/2*2 + (4+3)/2*2 = 6 + 7.
        self.assertEqual(expected_count_piecewise_linear(knots, 2.0, 6.0), 13.0)


class TestEmpiricalEstimates(unittest.TestCase):
    """EMPIRICAL: statistical estimates vs analytic expectations.

    Tolerances are set at ~5 standard errors with fixed seeds, so the runs
    are deterministic; conclusions here are estimates, not exact results.
    """

    def test_empirical_interarrival_mean(self):
        # EMPIRICAL ESTIMATE: mean inter-arrival ~ 1/rate (exact value 0.5).
        sim = PoissonSimulator(seed=2026)
        events = sim.generate_homogeneous(2.0, 0.0, 400.0)
        gaps = [b - a for a, b in zip(events, events[1:])]
        mean_gap = sum(gaps) / len(gaps)
        self.assertAlmostEqual(mean_gap, 0.5, delta=0.05)

    def test_empirical_count_matches_analytic_expectation(self):
        # EMPIRICAL ESTIMATE: mean count over paths ~ Lambda = 20 (exact).
        knots = [(0.0, 0.0), (10.0, 4.0)]
        exact = expected_count_piecewise_linear(knots)
        self.assertEqual(exact, 20.0)
        n_paths = 600
        master = ExponentialStream(seed=8)
        total = 0
        for _ in range(n_paths):
            path_seed = int(master.next() * 1e15)
            total += len(PoissonSimulator(seed=path_seed).generate_piecewise_linear(knots))
        mean_count = total / n_paths
        standard_error = math.sqrt(exact / n_paths)
        self.assertAlmostEqual(mean_count, exact, delta=5.0 * standard_error)


if __name__ == "__main__":
    unittest.main()
