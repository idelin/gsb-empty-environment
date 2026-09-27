"""unittest suite for poisson_process (Python 3.11, standard library only).

Tests labelled EXACT verify analytic/deterministic facts; tests labelled
EMPIRICAL verify statistical estimates against the analytic reference
with a fixed seed and a generous tolerance.
"""

import random
import unittest

from poisson_process import (
    ExpStream,
    Simulator,
    homogeneous,
    piecewise_constant,
    piecewise_linear,
)


class ScriptedRng:
    """Deterministic caller-injected source mimicking random.Random."""

    def __init__(self, values):
        self._values = list(values)
        self._index = 0

    def expovariate(self, lambd):
        value = self._values[self._index]
        self._index += 1
        return value / lambd


def simulate_all(intensity, seed):
    stream = ExpStream.from_seed(seed)
    sim = Simulator(intensity, stream)
    return sim.generate_until(intensity.t_end), stream


def simulate_chunked(intensity, seed, cuts):
    stream = ExpStream.from_seed(seed)
    sim = Simulator(intensity, stream)
    events = []
    for cut in cuts:
        events.extend(sim.generate_until(min(cut, intensity.t_end)))
    if sim.now < intensity.t_end:
        events.extend(sim.generate_until(intensity.t_end))
    return events


class HomogeneousTest(unittest.TestCase):
    def test_exact_exponential_interarrivals(self):
        # EXACT: scripted Exp(1) draws map to Exp(rate) gaps deterministically.
        stream = ExpStream(ScriptedRng([0.5, 0.25, 1.0, 50.0]))
        sim = Simulator(homogeneous(2.0, 0.0, 10.0), stream)
        events = sim.generate_until(10.0)
        # hazard levels 0.5, 0.75, 1.75 -> times 0.25, 0.375, 0.875
        self.assertEqual(events, [0.25, 0.375, 0.875])

    def test_same_seed_replays_identically(self):
        # EXACT: same seed -> identical event list and identical draw count.
        first, stream_a = simulate_all(homogeneous(2.0, 0.0, 10.0), seed=42)
        second, stream_b = simulate_all(homogeneous(2.0, 0.0, 10.0), seed=42)
        self.assertEqual(first, second)
        self.assertEqual(stream_a.draws, stream_b.draws)
        self.assertEqual(stream_a.seed, 42)

    def test_draws_and_seed_recorded(self):
        # EXACT bookkeeping: one initial draw plus one per emitted event.
        events, stream = simulate_all(homogeneous(2.0, 0.0, 10.0), seed=7)
        self.assertEqual(stream.draws, len(events) + 1)


class PiecewiseConstantTest(unittest.TestCase):
    def test_zero_intensity_interval_skipped(self):
        # EXACT: a long zero-rate gap yields no events and no hazard.
        intensity = piecewise_constant([0.0, 1.0, 100.0, 101.0], [2.0, 0.0, 2.0])
        self.assertEqual(intensity.expected_count(), 4.0)
        events, _ = simulate_all(intensity, seed=11)
        self.assertTrue(events)
        self.assertTrue(all(t <= 1.0 or t > 100.0 for t in events))

    def test_boundary_event_not_duplicated(self):
        # EXACT: scripted draw lands the first event exactly on the
        # breakpoint t=1.0; it is emitted exactly once across chunks.
        intensity = piecewise_constant([0.0, 1.0, 2.0], [1.0, 1.0])
        whole_stream = ExpStream(ScriptedRng([1.0, 0.4, 0.4, 50.0]))
        whole = Simulator(intensity, whole_stream).generate_until(2.0)
        self.assertEqual(whole[0], 1.0)

        chunk_stream = ExpStream(ScriptedRng([1.0, 0.4, 0.4, 50.0]))
        sim = Simulator(intensity, chunk_stream)
        first = sim.generate_until(1.0)
        second = sim.generate_until(2.0)
        self.assertEqual(first + second, whole)
        self.assertEqual((first + second).count(1.0), 1)

    def test_negative_rate_rejected(self):
        with self.assertRaises(ValueError):
            piecewise_constant([0.0, 1.0], [-0.5])
        with self.assertRaises(ValueError):
            homogeneous(-1.0, 0.0, 1.0)


class PiecewiseLinearTest(unittest.TestCase):
    def test_expected_count_is_analytic_integral(self):
        # EXACT: tent intensity integrates to two triangles of area 1.
        intensity = piecewise_linear([0.0, 1.0, 2.0], [0.0, 2.0, 0.0])
        self.assertEqual(intensity.expected_count(), 2.0)
        self.assertAlmostEqual(intensity.expected_count(0.5), 0.25)
        self.assertAlmostEqual(intensity.expected_count(1.5), 1.75)

    def test_stable_inversion_tiny_slope(self):
        # EXACT: with a tiny slope the inverse matches the constant-rate
        # limit; the naive quadratic formula would lose precision here.
        intensity = piecewise_linear([0.0, 10.0], [1.0, 1.0 + 1e-12])
        self.assertAlmostEqual(intensity.hazard_to_time(5.0), 5.0, places=9)

    def test_inversion_decreasing_intensity(self):
        # EXACT: lambda(x) = 2 - 2x, hazard H(x) = 2x - x^2,
        # H(x) = 0.75 has the in-segment root x = 0.5.
        intensity = piecewise_linear([0.0, 1.0], [2.0, 0.0])
        self.assertAlmostEqual(intensity.hazard_to_time(0.75), 0.5, places=15)

    def test_inversion_roundtrip(self):
        # EXACT: expected_count inverts hazard_to_time to machine precision.
        intensity = piecewise_linear([0.0, 1.0, 3.0, 4.0], [0.5, 2.0, 0.0, 1.0])
        rng = random.Random(99)
        for _ in range(200):
            h = rng.random() * intensity.total_hazard
            t = intensity.hazard_to_time(h)
            self.assertAlmostEqual(intensity.expected_count(t), h, places=12)

    def test_negative_value_rejected(self):
        with self.assertRaises(ValueError):
            piecewise_linear([0.0, 1.0, 2.0], [1.0, -0.1, 1.0])

    def test_invalid_geometry_rejected(self):
        with self.assertRaises(ValueError):
            piecewise_linear([0.0, 0.0, 1.0], [1.0, 1.0, 1.0])
        with self.assertRaises(ValueError):
            piecewise_linear([0.0, 1.0], [1.0])


class ChunkConsistencyTest(unittest.TestCase):
    PROCESSES = [
        homogeneous(2.0, 0.0, 10.0),
        piecewise_constant([0.0, 1.0, 100.0, 101.0], [2.0, 0.0, 2.0]),
        piecewise_linear([0.0, 1.0, 3.0, 4.0], [0.5, 2.0, 0.0, 1.0]),
    ]

    def test_chunked_equals_whole_same_exponential_sequence(self):
        # EXACT: identical exponential sequence -> identical events,
        # whether generated in one shot or in chunks (including chunks
        # that span the long zero-intensity gap).
        for intensity in self.PROCESSES:
            whole, _ = simulate_all(intensity, seed=1234)
            chunked = simulate_chunked(intensity, seed=1234, cuts=[0.3, 1.0, 50.0])
            self.assertEqual(whole, chunked)


class CheckpointTest(unittest.TestCase):
    def test_checkpoint_restore_recovers_exactly(self):
        # EXACT: restore from a checkpoint continues the identical run.
        intensity = piecewise_linear([0.0, 1.0, 3.0, 4.0], [0.5, 2.0, 0.0, 1.0])

        stream = ExpStream.from_seed(2026)
        sim = Simulator(intensity, stream)
        head = sim.generate_until(2.5)
        checkpoint = sim.checkpoint()
        tail = sim.generate_until(4.0)

        restored = Simulator.restore(intensity, ExpStream.from_seed(0), checkpoint)
        self.assertEqual(restored.generate_until(4.0), tail)
        self.assertEqual(restored.emitted, sim.emitted)
        self.assertEqual(checkpoint["seed"], 2026)
        self.assertEqual(checkpoint["draws"], len(head) + 1)

    def test_checkpoint_matches_uninterrupted_run(self):
        # EXACT: run -> checkpoint -> resume equals one uninterrupted run.
        intensity = piecewise_constant([0.0, 1.0, 100.0, 101.0], [2.0, 0.0, 2.0])
        whole, _ = simulate_all(intensity, seed=5)

        stream = ExpStream.from_seed(5)
        sim = Simulator(intensity, stream)
        head = sim.generate_until(0.5)
        checkpoint = sim.checkpoint()
        resumed = Simulator.restore(intensity, ExpStream.from_seed(999), checkpoint)
        self.assertEqual(head + resumed.generate_until(101.0), whole)


class IntensitySwitchTest(unittest.TestCase):
    def test_switch_keeps_events_and_stream(self):
        # EXACT: switching the intensity version at t=3 keeps the events
        # already emitted (no resampling) and equals a fresh run on the
        # combined piecewise process with the same seed.
        reference, _ = simulate_all(
            piecewise_constant([0.0, 3.0, 10.0], [1.0, 4.0]), seed=77
        )

        stream = ExpStream.from_seed(77)
        sim = Simulator(homogeneous(1.0, 0.0, 10.0), stream)
        kept = sim.generate_until(3.0)
        sim.switch_intensity(piecewise_constant([3.0, 10.0], [4.0]))
        combined = kept + sim.generate_until(10.0)

        self.assertEqual(combined, reference)
        self.assertEqual([t for t in combined if t <= 3.0], kept)

    def test_switch_requires_current_time(self):
        sim = Simulator(homogeneous(1.0, 0.0, 10.0), ExpStream.from_seed(1))
        sim.generate_until(2.0)
        with self.assertRaises(ValueError):
            sim.switch_intensity(homogeneous(2.0, 0.0, 10.0), at_time=5.0)


class EmpiricalTest(unittest.TestCase):
    def test_empirical_mean_matches_analytic_expectation(self):
        # EXACT reference: E[N] = rate * T = 10.0 analytically.
        # EMPIRICAL estimate: mean over 4000 replays (seed 777), tol ~5 sigma.
        intensity = homogeneous(2.5, 0.0, 4.0)
        exact = intensity.expected_count()
        self.assertEqual(exact, 10.0)

        rng = random.Random(777)
        replays = 4000
        total = 0
        for _ in range(replays):
            stream = ExpStream(rng, seed=777)
            total += len(Simulator(intensity, stream).generate_until(4.0))
        empirical = total / replays
        self.assertAlmostEqual(empirical, exact, delta=0.25)

    def test_empirical_mean_with_zero_gap(self):
        # EXACT reference: 2*1 + 0*99 + 2*1 = 4.0 despite the long gap.
        # EMPIRICAL estimate: mean over 4000 replays (seed 888).
        intensity = piecewise_constant([0.0, 1.0, 100.0, 101.0], [2.0, 0.0, 2.0])
        exact = intensity.expected_count()
        self.assertEqual(exact, 4.0)

        rng = random.Random(888)
        replays = 4000
        total = 0
        for _ in range(replays):
            stream = ExpStream(rng, seed=888)
            total += len(Simulator(intensity, stream).generate_until(101.0))
        empirical = total / replays
        self.assertAlmostEqual(empirical, exact, delta=0.16)


if __name__ == "__main__":
    unittest.main()
