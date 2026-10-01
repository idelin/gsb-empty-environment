'''Exact distribution proofs by enumerating the random-bit space.

These tests never use frequency counts. The argument has two parts:

1. tests.test_sources proves randbelow is exactly uniform on [0, n) by
   enumerating the bit space with an exact geometric rejection tail.
2. This file proves that, given exactly uniform ranks, the sampler maps
   them to keys with exactly the claimed probabilities: single draws are
   checked end-to-end over the bit space (homogeneous rejection, exact
   tail), and multi-draw without-replacement procedures are checked by
   enumerating every uniform-rank sequence.
'''

import unittest
from fractions import Fraction
from itertools import permutations

from weighted_sampler import BitSource, WeightedSampler, sequence_probability

from tests.exact_enum import exact_distribution, exact_distribution_over_ranks


def build(source, weights):
    sampler = WeightedSampler(source)
    for key, w in weights.items():
        sampler.add(key, w)
    return sampler


class SingleDrawExactTest(unittest.TestCase):
    WEIGHTS = {'a': Fraction(1, 2), 'b': Fraction(1, 3), 'c': Fraction(1, 6)}

    def test_single_draw_distribution_is_exact(self):
        # Scaled weights 3, 2, 1 over N=6; k=3 bits; rejection prob 1/4
        # per attempt; depth 9 = 3 attempts -> tail (1/4)**3.
        dist, tail = exact_distribution(
            lambda src: build(src, self.WEIGHTS).sample(), max_depth=9)
        self.assertEqual(sum(dist.values()) + tail, 1)
        self.assertEqual(tail, Fraction(1, 4) ** 3)
        total = sum(self.WEIGHTS.values())
        self.assertEqual(set(dist), set(self.WEIGHTS))
        for key, w in self.WEIGHTS.items():
            self.assertEqual(dist[key] / (1 - tail), w / total, key)

    def test_zero_weight_is_never_selected(self):
        weights = {'a': 0, 'b': 2, 'c': 0, 'd': 1}
        dist, tail = exact_distribution(
            lambda src: build(src, weights).sample(), max_depth=8)
        self.assertNotIn('a', dist)
        self.assertNotIn('c', dist)
        self.assertEqual(dist['b'] / (1 - tail), Fraction(2, 3))
        self.assertEqual(dist['d'] / (1 - tail), Fraction(1, 3))


class WithoutReplacementExactTest(unittest.TestCase):
    WEIGHTS = {'a': Fraction(2), 'b': Fraction(1), 'c': Fraction(1)}

    @staticmethod
    def draw_pair():
        tx = build(BitSource([]), WithoutReplacementExactTest.WEIGHTS).begin()
        return (tx.draw(), tx.draw())

    def test_ordered_pair_probabilities_are_exact(self):
        dist = exact_distribution_over_ranks(self.draw_pair)
        self.assertEqual(sum(dist.values()), 1)
        expected = set(permutations(self.WEIGHTS, 2))
        self.assertEqual(set(dist), expected)
        for seq in expected:
            self.assertEqual(dist[seq], sequence_probability(self.WEIGHTS, seq), seq)

    def test_first_draw_marginal_is_proportional_to_weight(self):
        dist = exact_distribution_over_ranks(self.draw_pair)
        marginal = {k: Fraction(0) for k in self.WEIGHTS}
        for seq, p in dist.items():
            marginal[seq[0]] += p
        for key, w in self.WEIGHTS.items():
            self.assertEqual(marginal[key], w / 4, key)

    def test_no_outcome_repeats_a_key(self):
        dist = exact_distribution_over_ranks(self.draw_pair)
        for seq in dist:
            self.assertEqual(len(set(seq)), len(seq))


class FullPermutationExactTest(unittest.TestCase):
    WEIGHTS = {'a': Fraction(1), 'b': Fraction(1), 'c': Fraction(2)}

    def test_draw_all_permutation_probabilities_are_exact(self):
        def run():
            tx = build(BitSource([]), self.WEIGHTS).begin()
            return (tx.draw(), tx.draw(), tx.draw())

        dist = exact_distribution_over_ranks(run)
        self.assertEqual(sum(dist.values()), 1)
        expected = set(permutations(self.WEIGHTS))
        self.assertEqual(set(dist), expected)
        for seq in expected:
            self.assertEqual(dist[seq], sequence_probability(self.WEIGHTS, seq), seq)


if __name__ == '__main__':
    unittest.main()
