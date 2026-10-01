'''Tests for WeightedSampler: weights, intervals, transactions, rollback.

All randomness in this file comes from fixed BitSource streams or seeded
sources, so every expected value is manually recomputable.
'''

import unittest
from fractions import Fraction

from weighted_sampler import (
    BitSource,
    EmptySamplerError,
    SeededRandomSource,
    StaleTransactionError,
    WeightedSampler,
    sequence_probability,
)


def make(weights, bits=None):
    '''Build a sampler over a deterministic bit stream (default: zeros).'''
    source = BitSource(bits if bits is not None else [0] * 4096)
    sampler = WeightedSampler(source)
    for key, w in weights:
        sampler.add(key, w)
    return sampler


class WeightValidationTest(unittest.TestCase):
    def test_negative_weight_rejected(self):
        s = WeightedSampler(BitSource([0]))
        with self.assertRaises(ValueError):
            s.add('a', Fraction(-1, 2))
        s.add('a', 1)
        with self.assertRaises(ValueError):
            s.update('a', -1)

    def test_float_weight_rejected_as_inexact(self):
        s = WeightedSampler(BitSource([0]))
        with self.assertRaises(TypeError):
            s.add('a', 0.1)

    def test_total_is_exact_rational(self):
        s = make([('a', Fraction(1, 3)), ('b', Fraction(1, 7)), ('c', 2)])
        self.assertEqual(s.total(), Fraction(1, 3) + Fraction(1, 7) + 2)
        s.update('b', Fraction(3, 5))
        self.assertEqual(s.total(), Fraction(1, 3) + Fraction(3, 5) + 2)
        s.remove('c')
        self.assertEqual(s.total(), Fraction(1, 3) + Fraction(3, 5))

    def test_zero_total_fails_explicitly(self):
        s = WeightedSampler(BitSource([0] * 64))
        with self.assertRaises(EmptySamplerError):
            s.sample()
        s.add('a', 0)
        s.add('b', 0)
        with self.assertRaises(EmptySamplerError):
            s.sample()
        with self.assertRaises(EmptySamplerError):
            s.draw()
        tx = s.begin()
        with self.assertRaises(EmptySamplerError):
            tx.draw()
        tx.rollback()

    def test_duplicate_add_and_missing_key(self):
        s = make([('a', 1)])
        with self.assertRaises(KeyError):
            s.add('a', 2)
        with self.assertRaises(KeyError):
            s.update('missing', 1)
        with self.assertRaises(KeyError):
            s.remove('missing')


class IntervalBoundaryTest(unittest.TestCase):
    '''Interval boundaries stay correct after arbitrary updates.

    Slot i owns the half-open rank interval [prefix(i), prefix(i+1)).
    We check lower_bound at every rank r in [0, N) against a naive
    cumulative-array computation, after a mixed update sequence.
    '''

    def test_boundaries_match_naive_cumulative_after_updates(self):
        s = make([('a', 1), ('b', 2), ('c', 0), ('d', 3)])
        s.update('b', 0)          # shrink a middle interval to empty
        s.update('c', 2)          # grow an empty interval
        s.remove('a')             # remove the first interval
        s.add('e', Fraction(1, 2))  # denominator change -> rescale
        s.update('d', Fraction(5, 2))
        # slot order after remove/add: e reuses slot 0 -> slots e,b,c,d;
        # scaled weights: e=1/2->1, b=0, c=2->4, d=5/2->5, N=10.
        self.assertEqual(s.total(), Fraction(2) + Fraction(5, 2) + Fraction(1, 2))
        naive = []
        for slot, key in enumerate(s._slot_to_key):
            if key is not None:
                naive += [key] * int(s._slots[slot] * s._denom)
        self.assertEqual(len(naive), s._tree.total())
        for r in range(s._tree.total()):
            slot = s._tree.lower_bound(r)
            self.assertEqual(s._slot_to_key[slot], naive[r], f'rank {r}')

    def test_zero_weight_owns_empty_interval(self):
        s = make([('a', 0), ('b', 1), ('c', 0)])
        for r in range(s._tree.total()):
            self.assertEqual(s._slot_to_key[s._tree.lower_bound(r)], 'b')


class ManualRecomputationTest(unittest.TestCase):
    '''Fixed random numbers, manually recomputable outcomes.'''

    def test_fixed_bits_select_by_prefix_interval(self):
        # weights a=1, b=2, c=3 -> N=6, k=3 bits; bits 101 = 5.
        # Prefix sums: a->1, b->3, c->6. r=5 falls in [3, 6) -> 'c'.
        s = make([('a', 1), ('b', 2), ('c', 3)], bits=[1, 0, 1])
        self.assertEqual(s.sample(), 'c')
        self.assertEqual(s._source.consumed, 3)

    def test_rejection_then_acceptance_is_recomputable(self):
        # N=6, k=3. Bits 110 = 6 -> rejected (>= 6). Bits 010 = 2 -> [1,3) -> 'b'.
        s = make([('a', 1), ('b', 2), ('c', 3)], bits=[1, 1, 0, 0, 1, 0])
        self.assertEqual(s.sample(), 'b')
        self.assertEqual(s._source.consumed, 6)

    def test_rational_weights_scale_to_integer_ranks(self):
        # weights 1/2, 1/3, 1/6 -> D=6 -> scaled 3, 2, 1, N=6, k=3.
        # bits 000 = 0 -> [0,3) -> 'a'; bits 100 = 4 -> [3,5) -> 'b';
        # bits 101 = 5 -> [5,6) -> 'c'.
        s = make([('a', Fraction(1, 2)), ('b', Fraction(1, 3)),
                  ('c', Fraction(1, 6))],
                 bits=[0, 0, 0, 1, 0, 0, 1, 0, 1])
        self.assertEqual(s.sample(), 'a')
        self.assertEqual(s.sample(), 'b')
        self.assertEqual(s.sample(), 'c')


class WithoutReplacementTest(unittest.TestCase):
    def test_draws_never_repeat_and_exhaust(self):
        s = make([('a', 1), ('b', 2), ('c', 3)])
        tx = s.begin()
        drawn = [tx.draw(), tx.draw(), tx.draw()]
        self.assertEqual(len(set(drawn)), 3)
        self.assertEqual(set(drawn), {'a', 'b', 'c'})
        with self.assertRaises(EmptySamplerError):
            tx.draw()
        tx.commit()
        self.assertEqual(s.total(), 0)
        with self.assertRaises(EmptySamplerError):
            s.sample()

    def test_draw_zeroes_weight_but_keeps_key(self):
        s = make([('a', 1), ('b', 2)])
        first = s.draw()
        self.assertEqual(s.weight(first), 0)
        self.assertIn(first, s)
        self.assertEqual(s.total(), 2 if first == 'a' else 1)

    def test_conditional_probability_is_explicit(self):
        # weights a=2, b=1, c=1. Draw order c, a:
        # P = (1/4) * (2 / (4-1)) = 1/6, checked against the formula.
        weights = {'a': Fraction(2), 'b': Fraction(1), 'c': Fraction(1)}
        s = make(list(weights.items()))
        tx = s.begin()
        seq = [tx.draw(), tx.draw()]
        self.assertEqual(tx.probability(), sequence_probability(weights, seq))
        tx.rollback()
        self.assertEqual(sequence_probability(weights, ['c', 'a']), Fraction(1, 6))

    def test_sequence_probability_rejects_repeats(self):
        with self.assertRaises(KeyError):
            sequence_probability({'a': 1}, ['a', 'a'])


class TransactionTest(unittest.TestCase):
    def test_rollback_restores_weights_and_random_state(self):
        # Two identical samplers: one runs a rolled-back transaction, the
        # other does nothing; their subsequent samples must be identical,
        # proving both weights and random state were restored.
        weights = [('a', 1), ('b', 2), ('c', 3)]
        s1 = WeightedSampler(SeededRandomSource(99))
        s2 = WeightedSampler(SeededRandomSource(99))
        for key, w in weights:
            s1.add(key, w)
            s2.add(key, w)
        tx = s1.begin()
        tx.draw()
        tx.draw()
        tx.rollback()
        self.assertEqual(s1.total(), s2.total())
        self.assertEqual(dict(s1.items()), dict(s2.items()))
        self.assertEqual([s1.sample() for _ in range(5)],
                         [s2.sample() for _ in range(5)])

    def test_commit_consumes_random_state(self):
        # Documented rule: commit keeps the source advanced (bits consumed),
        # rollback restores the source to its begin()-time state.
        weights = [('a', 1), ('b', 2), ('c', 3)]
        committed = make(weights)
        rolled_back = make(weights)
        before = committed._source.consumed
        with committed.begin() as tx:
            tx.draw()
        self.assertGreater(committed._source.consumed, before)
        tx = rolled_back.begin()
        tx.draw()
        tx.rollback()
        self.assertEqual(rolled_back._source.consumed, before)

        # Same rule through seeded sources: after rollback the stream is
        # identical to that of an untouched sampler with the same seed.
        rolled = WeightedSampler(SeededRandomSource(7))
        pristine = WeightedSampler(SeededRandomSource(7))
        for s in (rolled, pristine):
            for key, w in weights:
                s.add(key, w)
        tx = rolled.begin()
        tx.draw()
        tx.rollback()
        self.assertEqual([rolled.sample() for _ in range(4)],
                         [pristine.sample() for _ in range(4)])

    def test_commit_persists_and_bumps_version_once(self):
        s = make([('a', 1), ('b', 2), ('c', 3)])
        v0 = s.version
        with s.begin() as tx:
            tx.draw()
            tx.draw()
            self.assertEqual(s.total(), 6)  # live state untouched until commit
            drawn_weights = [s.weight(k) for k in tx.drawn]
        self.assertEqual(s.version, v0 + 1)
        self.assertEqual(s.total(), 6 - sum(drawn_weights))
        self.assertEqual(sum(1 for _, w in s.items() if w == 0), 2)

    def test_stale_commit_raises_and_rollback_still_works(self):
        s = make([('a', 1), ('b', 2)])
        tx = s.begin()
        tx.draw()
        s.update('a', 5)  # concurrent update bumps the version
        with self.assertRaises(StaleTransactionError):
            tx.commit()
        tx.rollback()  # transaction is still open; rollback restores RNG
        self.assertEqual(s.weight('a'), 5)
        self.assertEqual(s.weight('b'), 2)

    def test_context_manager_rolls_back_on_exception(self):
        s = make([('a', 1), ('b', 2)])
        v0 = s.version
        with self.assertRaises(RuntimeError):
            with s.begin() as tx:
                tx.draw()
                raise RuntimeError('boom')
        self.assertEqual(s.total(), 3)
        self.assertEqual(s.version, v0)

    def test_closed_transaction_rejects_use(self):
        s = make([('a', 1)])
        tx = s.begin()
        tx.rollback()
        with self.assertRaises(RuntimeError):
            tx.draw()
        with self.assertRaises(RuntimeError):
            tx.commit()

    def test_version_bumps_on_each_mutation(self):
        s = WeightedSampler(BitSource([0] * 64))
        self.assertEqual(s.version, 0)
        s.add('a', 1)
        s.add('b', 1)
        s.update('a', 2)
        s.remove('b')
        self.assertEqual(s.version, 4)


if __name__ == '__main__':
    unittest.main()
