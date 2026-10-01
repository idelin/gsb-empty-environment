"""Tests for unbiased range sampling (rejection sampling, big integers)."""

import collections
import unittest

from unbiased_shuffle import SplitMix64Source, randbelow


class CyclingBitSource:
    """Deterministic bit source cycling through all L-bit values in order.

    Used to verify rejection sampling EXACTLY: over each full cycle of
    2**L draws, every value in [0, n) is accepted exactly once.
    """

    def __init__(self, bits):
        self.bits = bits
        self.x = 0

    def getrandbits(self, k):
        assert k == self.bits, (k, self.bits)
        value = self.x
        self.x = (self.x + 1) % (1 << self.bits)
        return value


class ExactUniformityTest(unittest.TestCase):
    """EXACT conclusions: exhaustive enumeration of all bit draws."""

    def test_every_value_has_exactly_one_preimage_per_cycle(self):
        for n in range(2, 20):
            bits = n.bit_length()
            src = CyclingBitSource(bits)
            cycles = 3
            counts = collections.Counter(
                randbelow(src, n) for _ in range(n * cycles)
            )
            # After an integral number of cycles each value appears exactly
            # `cycles` times: rejection sampling is exactly uniform.
            self.assertEqual(counts, {v: cycles for v in range(n)})

    def test_exactly_one_preimage_per_value(self):
        # The exactness argument itself: with L = n.bit_length() bits, the
        # accepted draws are exactly {0, ..., n-1}, each occurring once.
        for n in range(2, 65):
            bits = n.bit_length()
            accepted = [x for x in range(1 << bits) if x < n]
            self.assertEqual(accepted, list(range(n)))

    def test_degenerate_and_invalid_ranges(self):
        src = SplitMix64Source(1)
        self.assertEqual(randbelow(src, 1), 0)
        draws_before = src.draws
        randbelow(src, 1)
        self.assertEqual(src.draws, draws_before)  # n == 1 draws nothing
        for bad in (0, -1, -10**50):
            with self.assertRaises(ValueError):
                randbelow(src, bad)


class BigIntegerTest(unittest.TestCase):
    """Ranges far beyond 64 bits must work and stay deterministic."""

    def test_huge_range(self):
        n = 2**256 + 2**130 + 7
        src = SplitMix64Source(2024)
        values = [src.randbelow(n) for _ in range(500)]
        self.assertTrue(all(0 <= v < n for v in values))
        self.assertGreater(len(set(values)), 400)  # not degenerate

    def test_deterministic_for_same_seed(self):
        n = 10**100 + 12345
        a = [SplitMix64Source(7).randbelow(n) for _ in range(0)]  # noqa: F841
        src_a, src_b = SplitMix64Source(7), SplitMix64Source(7)
        seq_a = [src_a.randbelow(n) for _ in range(50)]
        seq_b = [src_b.randbelow(n) for _ in range(50)]
        self.assertEqual(seq_a, seq_b)

    def test_state_roundtrip_preserves_stream(self):
        n = 2**200 + 99
        src = SplitMix64Source(99)
        head = [src.randbelow(n) for _ in range(10)]
        state = src.getstate()
        tail = [src.randbelow(n) for _ in range(10)]
        restored = SplitMix64Source(0)
        restored.setstate(state)
        self.assertEqual([restored.randbelow(n) for _ in range(10)], tail)
        self.assertEqual(len(head), 10)


class EmpiricalUniformityTest(unittest.TestCase):
    """EMPIRICAL estimate (not a proof): chi-square on sampled frequencies."""

    def test_chi_square_against_uniform(self):
        n, draws = 5, 50_000
        src = SplitMix64Source(31337)
        counts = collections.Counter(src.randbelow(n) for _ in range(draws))
        expected = draws / n
        chi2 = sum((counts[v] - expected) ** 2 / expected for v in range(n))
        # df = 4, 1% critical value = 13.277; deterministic for this seed.
        self.assertLess(chi2, 13.277, f"empirical chi2={chi2:.3f}")


if __name__ == "__main__":
    unittest.main()
