'''Tests for random sources and bias-free randbelow. Seed: fixed bit
patterns and seed 1234; every distribution check here is exact.'''

import unittest
from fractions import Fraction

from weighted_sampler import (
    BitSource,
    ExhaustedBitsError,
    SeededRandomSource,
    randbelow,
)

from tests.exact_enum import exact_distribution


class RandbelowRejectionTest(unittest.TestCase):
    def test_rejects_out_of_range_values(self):
        # n=3 -> k=2 bits. Bits 11 (=3, rejected) then 01 (=1, accepted).
        source = BitSource([1, 1, 0, 1])
        self.assertEqual(randbelow(source, 3), 1)
        self.assertEqual(source.consumed, 4)

    def test_power_of_two_never_rejects(self):
        source = BitSource([1, 0, 1, 0, 1, 1, 0, 0, 0])
        self.assertEqual(randbelow(source, 8), 0b101)
        self.assertEqual(randbelow(source, 8), 0b011)
        self.assertEqual(randbelow(source, 8), 0b000)
        self.assertEqual(source.consumed, 9)

    def test_invalid_n_raises(self):
        with self.assertRaises(ValueError):
            randbelow(BitSource([0] * 64), 0)

    def test_exact_uniformity_n3(self):
        # Exact argument, not frequency: every 2-bit prefix is enumerated;
        # value 3 is rejected, so the tail after 6 attempts is (1/4)**6.
        dist, tail = exact_distribution(lambda s: randbelow(s, 3), max_depth=12)
        self.assertEqual(sum(dist.values()) + tail, 1)
        self.assertEqual(tail, Fraction(1, 4) ** 6)
        for value in (0, 1, 2):
            self.assertEqual(dist[value] / (1 - tail), Fraction(1, 3))

    def test_exact_uniformity_n5(self):
        # k=3 bits, rejection probability 3/8 per attempt, depth 12 = 4 attempts.
        dist, tail = exact_distribution(lambda s: randbelow(s, 5), max_depth=12)
        self.assertEqual(sum(dist.values()) + tail, 1)
        self.assertEqual(tail, Fraction(3, 8) ** 4)
        for value in range(5):
            self.assertEqual(dist[value] / (1 - tail), Fraction(1, 5))


class BitSourceTest(unittest.TestCase):
    def test_from_int_matches_manual_bits(self):
        source = BitSource.from_int(0b101, 3)
        self.assertEqual(source.randbits(3), 5)
        self.assertEqual(BitSource.from_int(5, 3)._bits, [1, 0, 1])

    def test_exhaustion_and_state_restore(self):
        source = BitSource([1, 0])
        state = source.getstate()
        self.assertEqual(source.randbits(2), 2)
        with self.assertRaises(ExhaustedBitsError):
            source.randbits(1)
        source.setstate(state)
        self.assertEqual(source.randbits(2), 2)

    def test_rejects_non_binary_bits(self):
        with self.assertRaises(ValueError):
            BitSource([0, 2])


class SeededRandomSourceTest(unittest.TestCase):
    def test_same_seed_same_stream_and_seed_recorded(self):
        a = SeededRandomSource(1234)
        b = SeededRandomSource(1234)
        self.assertEqual(a.seed, 1234)
        self.assertEqual([a.randbits(16) for _ in range(5)],
                         [b.randbits(16) for _ in range(5)])

    def test_setstate_restores_stream(self):
        a = SeededRandomSource(1234)
        state = a.getstate()
        first = [a.randbits(8) for _ in range(4)]
        a.setstate(state)
        self.assertEqual([a.randbits(8) for _ in range(4)], first)


if __name__ == '__main__':
    unittest.main()
