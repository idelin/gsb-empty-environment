"""Tests for the pausable in-memory Fisher-Yates shuffle."""

import collections
import itertools
import math
import unittest

from unbiased_shuffle import PausableShuffle, SplitMix64Source, fisher_yates


class StepRangeTest(unittest.TestCase):
    """Every step must sample from the correct index range [0, frontier]."""

    def test_trace_ranges_and_frontier_progression(self):
        n = 25
        sh = PausableShuffle(list(range(n)), SplitMix64Source(11))
        sh.run()
        frontiers = [t[0] for t in sh.trace]
        self.assertEqual(frontiers, list(range(n - 1, 0, -1)))
        for i, j in sh.trace:
            self.assertTrue(0 <= j <= i)
        self.assertEqual(sh.samples, n - 1)  # exactly one sample per step
        self.assertEqual(sorted(sh.items), list(range(n)))  # still a permutation

    def test_empty_and_singleton(self):
        for items in ([], [42]):
            sh = PausableShuffle(items, SplitMix64Source(1))
            sh.run()
            self.assertEqual(sh.items, list(items))
            self.assertEqual(sh.samples, 0)


class SnapshotTest(unittest.TestCase):
    """Pausing and restoring must be indistinguishable from a continuous run."""

    def test_restore_at_every_pause_point(self):
        n = 20
        items = list(range(n))
        reference = PausableShuffle(items, SplitMix64Source(1234))
        reference.run()
        for pause in range(0, n):
            paused = PausableShuffle(items, SplitMix64Source(1234))
            paused.run(max_steps=pause)
            snap = paused.snapshot()
            # Restore onto a source with a *wrong* state: restore must win.
            restored = PausableShuffle.restore(snap, SplitMix64Source(999))
            restored.run()
            self.assertEqual(restored.items, reference.items)
            self.assertEqual(restored.trace, reference.trace)
            self.assertEqual(restored.samples, reference.samples)

    def test_fisher_yates_wrapper_matches(self):
        items = list("abcdefgh")
        a = fisher_yates(items, SplitMix64Source(5))
        b = PausableShuffle(items, SplitMix64Source(5)).run()
        self.assertEqual(a, b)
        self.assertEqual(sorted(a), sorted(items))


class ExactEnumerationTest(unittest.TestCase):
    """EXACT proof for small n: enumerate every random branch.

    Fisher-Yates on n elements consumes one draw j_i in [0, i] for
    i = n-1 .. 1, i.e. exactly prod(i+1) = n! equally likely draw sequences.
    Enumerating all of them must yield each of the n! permutations exactly
    once, which proves uniformity exactly (not empirically).
    """

    def test_all_draw_sequences_hit_each_permutation_once(self):
        for n in range(1, 8):
            counts = collections.Counter()
            choice_ranges = [range(i + 1) for i in range(n - 1, 0, -1)]
            for draws in itertools.product(*choice_ranges):
                src = _ScriptedSource(draws)
                result = PausableShuffle(list(range(n)), src).run()
                counts[tuple(result)] += 1
            self.assertEqual(len(counts), math.factorial(n))
            self.assertTrue(all(c == 1 for c in counts.values()))
            self.assertEqual(set(counts), set(itertools.permutations(range(n))))


class _ScriptedSource:
    """Random source that replays a fixed draw sequence (for enumeration)."""

    def __init__(self, values):
        self.values = list(values)
        self.pos = 0

    def randbelow(self, n):
        value = self.values[self.pos]
        self.pos += 1
        assert 0 <= value < n
        return value

    def getstate(self):
        return {"pos": self.pos}

    def setstate(self, state):
        self.pos = state["pos"]


class EmpiricalUniformityTest(unittest.TestCase):
    """EMPIRICAL estimate (not a proof): permutation frequencies, n = 3."""

    def test_chi_square_over_permutations(self):
        n, trials = 3, 120_000
        rng = SplitMix64Source(2026)
        counts = collections.Counter(
            tuple(fisher_yates(range(n), rng)) for _ in range(trials)
        )
        self.assertEqual(len(counts), 6)
        expected = trials / 6
        chi2 = sum((c - expected) ** 2 / expected for c in counts.values())
        # df = 5, 1% critical value = 15.086; deterministic for this seed.
        self.assertLess(chi2, 15.086, f"empirical chi2={chi2:.3f}")


if __name__ == "__main__":
    unittest.main()
