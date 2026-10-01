'''Model-based test: Fenwick-tree sampler vs a naive cumulative-array
reference, driven by a deterministic operation sequence.

Op-sequence seed: 20261001 (recorded for reproducibility). Both sides
share identical seeded random sources, so every draw must match exactly,
including across transaction commits, rollbacks, and stale-commit
rejection. After every operation the full weight map, total, and version
are compared.
'''

import random
import unittest
from fractions import Fraction
from math import lcm

from weighted_sampler import (
    EmptySamplerError,
    SeededRandomSource,
    StaleTransactionError,
    WeightedSampler,
    randbelow,
    sequence_probability,
)

OP_SEED = 20261001
NUM_OPS = 800


class RefModel:
    '''Naive reference: cumulative array, linear scan, same denom policy.'''

    def __init__(self, source):
        self.source = source
        self.slots = []
        self.keys = []
        self.free = []
        self.denom = 1
        self.version = 0

    def _track_denom(self, w):
        if self.denom % w.denominator:
            self.denom = lcm(self.denom, w.denominator)

    def add(self, key, w):
        self._track_denom(w)
        if self.free:
            i = self.free.pop()
        else:
            i = len(self.slots)
            self.slots.append(None)
            self.keys.append(None)
        self.slots[i] = w
        self.keys[i] = key
        self.version += 1

    def update(self, key, w):
        self._track_denom(w)
        self.slots[self.keys.index(key)] = w
        self.version += 1

    def remove(self, key):
        i = self.keys.index(key)
        self.slots[i] = None
        self.keys[i] = None
        self.free.append(i)
        self.version += 1

    def total(self):
        return sum((w for w in self.slots if w), Fraction(0))

    def weights(self):
        return {k: w for k, w in zip(self.keys, self.slots) if k is not None}

    def _pick(self, slots):
        scaled = [int(w * self.denom) if w else 0 for w in slots]
        r = randbelow(self.source, sum(scaled))
        acc = 0
        for i, x in enumerate(scaled):
            acc += x
            if r < acc:
                return i
        raise AssertionError('unreachable')

    def sample(self):
        if self.total() == 0:
            raise EmptySamplerError
        return self.keys[self._pick(self.slots)]

    def begin(self):
        return RefTx(self)


class RefTx:
    def __init__(self, model):
        self.model = model
        self.base_version = model.version
        self.rng_state = model.source.getstate()
        self.slots = list(model.slots)
        self.weights_at_begin = model.weights()
        self.drawn = []

    def draw(self):
        if sum((w for w in self.slots if w), Fraction(0)) == 0:
            raise EmptySamplerError
        i = self.model._pick(self.slots)
        self.slots[i] = Fraction(0)
        key = self.model.keys[i]
        self.drawn.append(key)
        return key

    def probability(self):
        return sequence_probability(self.weights_at_begin, self.drawn)

    def commit(self):
        if self.model.version != self.base_version:
            raise StaleTransactionError
        for key in self.drawn:
            self.model.slots[self.model.keys.index(key)] = Fraction(0)
        self.model.version += 1

    def rollback(self):
        self.model.source.setstate(self.rng_state)


class ModelComparisonTest(unittest.TestCase):
    def test_random_ops_match_reference(self):
        rng = random.Random(OP_SEED)
        sampler = WeightedSampler(SeededRandomSource(OP_SEED))
        model = RefModel(SeededRandomSource(OP_SEED))
        keys = ['k%d' % i for i in range(8)]

        def rand_weight():
            return Fraction(rng.randint(0, 8), rng.choice([1, 2, 3, 4, 6]))

        def check_state():
            self.assertEqual(dict(sampler.items()), model.weights())
            self.assertEqual(sampler.total(), model.total())
            self.assertEqual(sampler.version, model.version)

        def run_transaction():
            tx_s = sampler.begin()
            tx_m = model.begin()
            for _ in range(rng.randint(1, 3)):
                try:
                    got_s = tx_s.draw()
                except EmptySamplerError:
                    with self.assertRaises(EmptySamplerError):
                        tx_m.draw()
                    break
                self.assertEqual(got_s, tx_m.draw())
            self.assertEqual(tx_s.drawn, tuple(tx_m.drawn))
            self.assertEqual(tx_s.probability(), tx_m.probability())
            interfere = rng.random() < 0.15 and len(model.weights()) > 0
            if interfere:
                victim = rng.choice(sorted(model.weights()))
                new_w = rand_weight()
                sampler.update(victim, new_w)
                model.update(victim, new_w)
                with self.assertRaises(StaleTransactionError):
                    tx_s.commit()
                with self.assertRaises(StaleTransactionError):
                    tx_m.commit()
                tx_s.rollback()
                tx_m.rollback()
            elif rng.random() < 0.5:
                tx_s.commit()
                tx_m.commit()
            else:
                tx_s.rollback()
                tx_m.rollback()

        for _ in range(NUM_OPS):
            op = rng.random()
            live = sorted(model.weights())
            if op < 0.30:
                unused = [k for k in keys if k not in model.weights()]
                if not unused:
                    continue
                key, w = rng.choice(unused), rand_weight()
                sampler.add(key, w)
                model.add(key, w)
            elif op < 0.50:
                if not live:
                    continue
                key, w = rng.choice(live), rand_weight()
                sampler.update(key, w)
                model.update(key, w)
            elif op < 0.60:
                if not live:
                    continue
                key = rng.choice(live)
                sampler.remove(key)
                model.remove(key)
            elif op < 0.75:
                if model.total() == 0:
                    with self.assertRaises(EmptySamplerError):
                        sampler.sample()
                    with self.assertRaises(EmptySamplerError):
                        model.sample()
                else:
                    self.assertEqual(sampler.sample(), model.sample())
            else:
                run_transaction()
            check_state()

        self.assertGreater(sampler.version, 0)


if __name__ == '__main__':
    unittest.main()
