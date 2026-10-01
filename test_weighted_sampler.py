"""unittest suite for the exact weighted sampler.

Methodology note: unbiasedness is established by *exact enumeration* of the
random source's outcome space (all bit strings of a fixed length, or all
uniform integers r in [0, N)), never by frequency approximation. Every
expected count below is an exact integer derived in closed form.
"""

import random
import unittest
from fractions import Fraction

from random_sources import (
    BitsExhaustedError,
    BitStreamSource,
    ScriptedSource,
    SeededRandomSource,
)
from weighted_sampler import WeightedSampler, ZeroTotalWeightError


class RejectionSamplingExactTest(unittest.TestCase):
    """Exactness of rejection sampling: over all 2^(k*R) bit strings, every
    value in [0, n) is produced by exactly (2^(kR) - m^R)/n strings, where
    m = 2^k - n, and exactly m^R strings fail to terminate (undetermined).
    This is an exact partition of the outcome space, not a frequency check."""

    def test_uniform_over_all_bitstrings(self):
        for n, rounds in [(2, 8), (3, 8), (5, 5), (6, 5), (7, 5), (10, 4)]:
            k = n.bit_length()
            m = (1 << k) - n
            length = k * rounds
            total_streams = 1 << length
            counts = [0] * n
            undetermined = 0
            for v in range(total_streams):
                src = BitStreamSource(v, length)
                try:
                    counts[src.randbelow(n)] += 1
                except BitsExhaustedError:
                    undetermined += 1
            determined = total_streams - m ** rounds
            self.assertEqual(determined % n, 0)
            self.assertEqual(undetermined, m ** rounds, f"n={n}")
            self.assertEqual(counts, [determined // n] * n, f"n={n}")


class WeightedDrawExactTest(unittest.TestCase):
    """Exact distribution of a single weighted draw, by full enumeration of
    bit strings. Weights [0, 1/2, 1/3, 1/6] -> D=6, integer weights [0,3,2,1],
    N=6, k=3 bits/round, m=2. With R=5 rounds (15 bits), exactly
    2^15 - 2^5 = 32736 streams terminate, and index i must be produced by
    exactly 32736 * W_i / 6 streams."""

    WEIGHTS = [Fraction(0), Fraction(1, 2), Fraction(1, 3), Fraction(1, 6)]

    def test_exact_distribution_and_zero_weight(self):
        rounds = 5
        length = 3 * rounds
        sampler = WeightedSampler(self.WEIGHTS, source=ScriptedSource([]))
        counts = [0] * len(self.WEIGHTS)
        undetermined = 0
        for v in range(1 << length):
            sampler.source = BitStreamSource(v, length)
            try:
                counts[sampler.draw()] += 1
            except BitsExhaustedError:
                undetermined += 1
        determined = (1 << length) - 2 ** rounds
        self.assertEqual(undetermined, 2 ** rounds)
        expected = [determined * w // 6 for w in (0, 3, 2, 1)]
        self.assertEqual(counts, expected)
        self.assertEqual(counts[0], 0)  # weight 0 is never selected (exact)


class BoundaryMappingTest(unittest.TestCase):
    """Interval boundaries with fixed random numbers (manual recomputation).

    Weights [1/2, 1/3, 1/6]: D=6, N=6, intervals in units of r:
    index 0: r in [0,3)   index 1: r in [3,5)   index 2: r in [5,6)."""

    WEIGHTS = [Fraction(1, 2), Fraction(1, 3), Fraction(1, 6)]

    def test_fixed_random_numbers_recomputable(self):
        s = WeightedSampler(self.WEIGHTS, source=ScriptedSource([0, 2, 3, 4, 5]))
        self.assertEqual([s.draw() for _ in range(5)], [0, 0, 1, 1, 2])
        info = s.last_draw_info
        self.assertEqual(info, (5, 6, 6, 2))  # r=5, D=6, N=6 -> index 2
        lo, hi = s.intervals()[2]
        self.assertTrue(lo <= Fraction(info.r, info.denominator) < hi)

    def test_intervals_exact(self):
        s = WeightedSampler(self.WEIGHTS, source=ScriptedSource([]))
        self.assertEqual(
            s.intervals(),
            [
                (Fraction(0), Fraction(1, 2)),
                (Fraction(1, 2), Fraction(5, 6)),
                (Fraction(5, 6), Fraction(1)),
            ],
        )

    def test_boundary_ownership(self):
        s = WeightedSampler(self.WEIGHTS, source=ScriptedSource([]))
        self.assertEqual(s.locate(Fraction(0)), 0)
        self.assertEqual(s.locate(Fraction(1, 2)), 1)   # left interval is open
        self.assertEqual(s.locate(Fraction(5, 6)), 2)
        self.assertEqual(s.locate(Fraction(1, 3)), 0)
        with self.assertRaises(ValueError):
            s.locate(Fraction(1))       # == total: outside
        with self.assertRaises(ValueError):
            s.locate(Fraction(-1, 10))

    def test_boundaries_correct_after_update(self):
        s = WeightedSampler(self.WEIGHTS, source=ScriptedSource([0, 1, 2]))
        s.set_weight(0, Fraction(0))          # remaining: [0, 1/3, 1/6]
        self.assertEqual(s.total(), Fraction(1, 2))
        self.assertEqual(
            s.intervals(),
            [
                (Fraction(0), Fraction(0)),
                (Fraction(0), Fraction(1, 3)),
                (Fraction(1, 3), Fraction(1, 2)),
            ],
        )
        # D=6, N=3: r in [0,2) -> index 1, r in [2,3) -> index 2
        self.assertEqual([s.draw() for _ in range(3)], [1, 1, 2])
        self.assertEqual(s.locate(Fraction(0)), 1)  # zero-weight slot skipped


class WithoutReplacementExactTest(unittest.TestCase):
    """Exact conditional probabilities for an ordered batch of 2 draws
    without replacement. Weights [1, 2, 1], T=4 (D=1 at every step).
    Enumerating every (r1, r2) pair exactly: r1 in [0,4), then
    r2 in [0, 4 - w_i); each pair has probability 1/(4*(4 - w_i)).
    The aggregated distribution must equal w_i/T * w_j/(T - w_i) exactly."""

    WEIGHTS = [Fraction(1), Fraction(2), Fraction(1)]

    def test_ordered_pair_distribution_exact(self):
        probs = {}
        for r1 in range(4):
            s = WeightedSampler(self.WEIGHTS, source=ScriptedSource([r1]))
            i = s.draw()
            remaining = int(4 - self.WEIGHTS[i])
            for r2 in range(remaining):
                s2 = WeightedSampler(self.WEIGHTS, source=ScriptedSource([r1, r2]))
                tx = s2.begin()
                pair = tuple(tx.sample(2))
                tx.commit()
                self.assertEqual(pair[0], i)
                self.assertNotEqual(pair[0], pair[1])  # no duplicates
                p = Fraction(1, 4 * remaining)
                probs[pair] = probs.get(pair, Fraction(0)) + p
        expected = {}
        for i in range(3):
            for j in range(3):
                if i != j:
                    expected[(i, j)] = (
                        self.WEIGHTS[i] / 4 * self.WEIGHTS[j] / (4 - self.WEIGHTS[i])
                    )
        self.assertEqual(probs, expected)
        self.assertEqual(sum(probs.values()), 1)

    def test_committed_draws_stay_removed(self):
        s = WeightedSampler(self.WEIGHTS, source=SeededRandomSource(99))
        batch = s.sample_without_replacement(3)
        self.assertEqual(sorted(batch), [0, 1, 2])
        self.assertEqual(s.total(), 0)
        with self.assertRaises(ZeroTotalWeightError):
            s.draw()


class ZeroTotalWeightTest(unittest.TestCase):
    def test_explicit_failure_on_zero_total(self):
        for weights in ([], [0, 0, 0]):
            s = WeightedSampler(weights, source=SeededRandomSource(1))
            with self.assertRaises(ZeroTotalWeightError):
                s.draw()

    def test_zero_total_after_updates(self):
        s = WeightedSampler([Fraction(1), Fraction(2)], source=SeededRandomSource(1))
        s.remove(0)
        s.remove(1)
        with self.assertRaises(ZeroTotalWeightError):
            s.draw()

    def test_transaction_fails_when_exhausted_then_rolls_back(self):
        s = WeightedSampler([Fraction(1)], source=SeededRandomSource(5))
        tx = s.begin()
        self.assertEqual(tx.draw(), 0)
        with self.assertRaises(ZeroTotalWeightError):
            tx.draw()
        tx.rollback()
        self.assertEqual(s.total(), 1)
        self.assertEqual(s.weights, (Fraction(1),))


class RollbackTest(unittest.TestCase):
    def test_rollback_restores_weights_and_random_state(self):
        src = SeededRandomSource(7)
        s = WeightedSampler([Fraction(1, 3)] * 3, source=src)
        tx = s.begin()
        tx.sample(2)
        tx.rollback()
        self.assertEqual(s.weights, (Fraction(1, 3),) * 3)
        self.assertEqual(s.total(), 1)
        self.assertEqual(src.values_returned, 0)  # random values unconsumed
        self.assertEqual(src.bits_consumed, 0)
        # The rolled-back draws left no trace: identical to a fresh run.
        src2 = SeededRandomSource(7)
        s2 = WeightedSampler([Fraction(1, 3)] * 3, source=src2)
        self.assertEqual([s.draw() for _ in range(3)], [s2.draw() for _ in range(3)])

    def test_commit_consumes_weights_and_randomness(self):
        src = SeededRandomSource(11)
        s = WeightedSampler([Fraction(1)] * 4, source=src)
        tx = s.begin()
        drawn = tx.sample(2)
        tx.commit()
        self.assertEqual(len(set(drawn)), 2)
        self.assertEqual(s.total(), 2)
        for i in drawn:
            self.assertEqual(s.weight(i), 0)
        self.assertEqual(src.values_returned, 2)  # random values consumed
        with self.assertRaises(RuntimeError):
            tx.draw()

    def test_sample_without_replacement_rolls_back_on_failure(self):
        s = WeightedSampler([Fraction(1)] * 2, source=SeededRandomSource(3))
        with self.assertRaises(ZeroTotalWeightError):
            s.sample_without_replacement(3)
        self.assertEqual(s.total(), 2)  # failure left weights untouched


class SnapshotVersionTest(unittest.TestCase):
    def test_snapshot_freezes_weights(self):
        s = WeightedSampler(
            [Fraction(1), Fraction(1), Fraction(2)], source=ScriptedSource([])
        )
        snap = s.snapshot(source=ScriptedSource([0, 3]))
        self.assertEqual(snap.version, s.version)
        s.set_weight(0, Fraction(5))
        self.assertGreater(s.version, snap.version)
        self.assertEqual(snap.total(), 4)   # frozen
        self.assertEqual(s.total(), 8)      # live
        # Old intervals: [0,1), [1,2), [2,4); D=1, N=4.
        self.assertEqual(snap.draw(), 0)    # r=0
        self.assertEqual(snap.draw(), 2)    # r=3

    def test_versions_increase_monotonically(self):
        s = WeightedSampler([Fraction(1)], source=SeededRandomSource(1))
        v = s.version
        s.set_weight(0, Fraction(2))
        s.add(Fraction(1))
        tx = s.begin()
        tx.draw()
        tx.rollback()
        self.assertGreater(s.version, v + 2)  # set, add, rollback all bumped


class DynamicOpsVsCumulativeArrayTest(unittest.TestCase):
    """Random add/set/remove plus transactions, checked against a naive
    cumulative-array reference by enumerating *every* r in [0, N) (exact
    mapping check, not a frequency check)."""

    @staticmethod
    def ref_locate(weights, target):
        acc = Fraction(0)
        for i, w in enumerate(weights):
            acc += w
            if target < acc:
                return i
        raise AssertionError("target outside total")

    def assert_mapping_matches(self, sampler, ref):
        self.assertEqual(sampler.total(), sum(ref, Fraction(0)))
        self.assertEqual(sampler.weights, tuple(ref))
        if sampler.total() == 0:
            return
        d = sampler.common_denominator()
        n = (sampler.total() * d).numerator
        for r in range(n):
            target = Fraction(r, d)
            self.assertEqual(sampler.locate(target), self.ref_locate(ref, target))

    def test_random_operations(self):
        rng = random.Random(20261001)
        s = WeightedSampler([], source=SeededRandomSource(4242))
        ref = []
        for step in range(300):
            op = rng.choice(["add", "set", "remove", "tx_commit", "tx_rollback"])
            if op == "add" or not ref:
                w = Fraction(rng.randint(0, 5))
                ref.append(w)
                self.assertEqual(s.add(w), len(ref) - 1)
            elif op == "set":
                i = rng.randrange(len(ref))
                w = Fraction(rng.randint(0, 5))
                ref[i] = w
                s.set_weight(i, w)
            elif op == "remove":
                i = rng.randrange(len(ref))
                ref[i] = Fraction(0)
                s.remove(i)
            else:
                tx = s.begin()
                k = rng.randint(0, 2)
                drawn = []
                try:
                    for _ in range(k):
                        drawn.append(tx.draw())
                except ZeroTotalWeightError:
                    pass
                if op == "tx_commit":
                    tx.commit()
                    for i in drawn:
                        ref[i] = Fraction(0)
                else:
                    tx.rollback()
            if step % 20 == 0 or step == 299:
                self.assert_mapping_matches(s, ref)


class SeedAndConsumptionRecordingTest(unittest.TestCase):
    def test_seed_and_sample_size_recorded(self):
        src = SeededRandomSource(2024)
        s = WeightedSampler([Fraction(1, 2)] * 4, source=src)
        for _ in range(5):
            s.draw()
        self.assertEqual(src.seed, 2024)
        self.assertEqual(src.values_returned, 5)
        self.assertGreaterEqual(src.bits_consumed, 5 * 2)  # N=4 -> k=2 bits

    def test_same_seed_reproduces(self):
        def run():
            s = WeightedSampler(
                [Fraction(1, 6), Fraction(1, 3), Fraction(1, 2)],
                source=SeededRandomSource(31337),
            )
            return [s.draw() for _ in range(10)]

        self.assertEqual(run(), run())


class ValidationTest(unittest.TestCase):
    def test_rejects_negative_and_inexact_weights(self):
        with self.assertRaises(ValueError):
            WeightedSampler([Fraction(-1)], source=SeededRandomSource(1))
        with self.assertRaises(TypeError):
            WeightedSampler([0.5], source=SeededRandomSource(1))
        s = WeightedSampler([Fraction(1)], source=SeededRandomSource(1))
        with self.assertRaises(ValueError):
            s.set_weight(0, Fraction(-1))
        with self.assertRaises(TypeError):
            s.add(0.1)

    def test_index_errors_and_source_injection(self):
        s = WeightedSampler([Fraction(1)], source=SeededRandomSource(1))
        with self.assertRaises(IndexError):
            s.set_weight(5, Fraction(1))
        with self.assertRaises(ValueError):
            WeightedSampler([Fraction(1)])  # source must be injected


if __name__ == "__main__":
    unittest.main()
