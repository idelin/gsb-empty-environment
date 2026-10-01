'''Exact enumeration of the random-bit space.

Two complementary exact techniques, neither using frequencies:

* exact_distribution: enumerate every finite bit prefix. A prefix that
  fully determines an outcome contributes probability mass 2**-depth; a
  prefix that exhausts the bit stream is extended by one bit. Prefixes
  still unresolved at max_depth form the tail mass (rejection-sampling
  retries beyond the depth cap). Used to prove randbelow uniformity,
  where the rejection structure is homogeneous across outcomes.

* exact_distribution_over_ranks: intercept randbelow and enumerate every
  rank sequence (rank r in [0, n) at each call, adaptively). Combined
  with the proven exact uniformity of randbelow, this yields the exact
  distribution of any sampling procedure without rejection tails.
'''

from fractions import Fraction
from unittest.mock import patch

from weighted_sampler import BitSource, ExhaustedBitsError


def exact_distribution(run, max_depth):
    '''Return (dist, tail) for a sampling procedure.

    run(BitSource) -> hashable outcome, raising ExhaustedBitsError when
    it needs more bits than the prefix provides. dist maps outcome to
    its exact probability mass; tail is the exact unresolved mass.
    Invariant: sum(dist.values()) + tail == 1.
    '''
    dist = {}

    def rec(prefix):
        source = BitSource(prefix)
        try:
            outcome = run(source)
        except ExhaustedBitsError:
            if len(prefix) >= max_depth:
                return Fraction(1, 1 << len(prefix))
            return rec(prefix + [0]) + rec(prefix + [1])
        mass = Fraction(1, 1 << len(prefix))
        dist[outcome] = dist.get(outcome, Fraction(0)) + mass
        return Fraction(0)

    tail = rec([])
    return dist, tail


class _NeedRank(Exception):
    def __init__(self, n):
        self.n = n


def exact_distribution_over_ranks(run):
    '''Exact distribution of a sampling procedure over uniform ranks.

    run() must perform its draws through weighted_sampler.sampler; every
    randbelow(source, n) call is intercepted and the enumeration branches
    over all n uniformly likely ranks. A rank sequence [r_1, ...] with
    call sizes [n_1, ...] has probability prod(1 / n_j); the returned
    dist maps each outcome to its exact total probability (sums to 1).
    '''
    dist = {}

    def rec(script, prob):
        index = 0

        def fake_randbelow(source, n):
            nonlocal index
            if index == len(script):
                raise _NeedRank(n)
            value = script[index]
            index += 1
            assert 0 <= value < n
            return value

        try:
            with patch('weighted_sampler.sampler.randbelow', fake_randbelow):
                outcome = run()
        except _NeedRank as need:
            for value in range(need.n):
                rec(script + [value], prob / need.n)
            return
        dist[outcome] = dist.get(outcome, Fraction(0)) + prob

    rec([], Fraction(1))
    return dist
