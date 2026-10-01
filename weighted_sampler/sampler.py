'''Weighted sampling over exact rational weights with snapshot transactions.

Design notes
------------
* Weights are exact fractions.Fraction values (ints are converted).
  Floats are rejected because they are inexact.
* Internally all weights share a common denominator D (the lcm of all
  denominators seen so far). The Fenwick tree stores integer weights
  W_i = w_i * D; sampling draws a uniform integer r in
  [0, sum W_i) via rejection sampling and locates the slot whose
  cumulative interval contains r. Slot i is chosen with
  probability exactly W_i / sum W_i == w_i / total.
* A slot with weight 0 owns an empty interval, so it can never be picked.
* Transactions implement snapshot versioning: a transaction works on a
  private copy of the state captured at begin(); commit() succeeds
  only if the sampler version is unchanged (optimistic concurrency),
  otherwise StaleTransactionError is raised. rollback() discards
  the copy and restores the random-source state captured at begin().

Random-state consumption rule: commit() consumes the random draws
(the source stays advanced); rollback() restores the source to its
begin()-time state, so a cancelled transaction consumes nothing.
'''

from __future__ import annotations

from fractions import Fraction
from math import lcm

from .sources import randbelow


class EmptySamplerError(Exception):
    '''Raised when sampling from a sampler whose total weight is zero.'''


class StaleTransactionError(Exception):
    '''Raised when committing a transaction whose base version is stale.'''


def _as_weight(value) -> Fraction:
    if isinstance(value, Fraction):
        w = value
    elif isinstance(value, int):
        w = Fraction(value)
    else:
        raise TypeError(f'weight must be int or Fraction, got {type(value).__name__}')
    if w < 0:
        raise ValueError('weight must be non-negative')
    return w


class _Fenwick:
    '''Fenwick tree over non-negative integers with prefix lower_bound.'''

    def __init__(self, capacity: int):
        self.capacity = capacity
        self.t = [0] * (capacity + 1)

    def add(self, i: int, delta: int) -> None:
        i += 1
        while i <= self.capacity:
            self.t[i] += delta
            i += i & -i

    def prefix(self, i: int) -> int:
        '''Sum of cells [0, i).'''
        s = 0
        while i > 0:
            s += self.t[i]
            i -= i & -i
        return s

    def total(self) -> int:
        return self.prefix(self.capacity)

    def lower_bound(self, r: int) -> int:
        '''Smallest slot i with prefix(i + 1) > r; requires 0 <= r < total().'''
        i = 0
        bit = 1 << (self.capacity.bit_length() - 1) if self.capacity else 0
        while bit:
            nxt = i + bit
            if nxt <= self.capacity and self.t[nxt] <= r:
                r -= self.t[nxt]
                i = nxt
            bit >>= 1
        return i


def sequence_probability(weights, sequence) -> Fraction:
    '''Exact probability of an ordered without-replacement draw sequence.

    P(i_1, ..., i_k) = prod_j w_{i_j} / (T - w_{i_1} - ... - w_{i_{j-1}}),
    where T is the initial total. This is the explicit conditional-
    probability rule used by Transaction.draw().
    '''
    remaining = {k: _as_weight(v) for k, v in weights.items()}
    total = sum(remaining.values(), Fraction(0))
    prob = Fraction(1)
    for key in sequence:
        if key not in remaining:
            raise KeyError(f'{key!r} missing or already drawn')
        if total <= 0:
            raise EmptySamplerError('total weight is zero')
        w = remaining.pop(key)
        prob *= w / total
        total -= w
    return prob


class WeightedSampler:
    '''Fenwick-tree sampler over exact rational, non-negative weights.

    The random source is injected by the caller (see sources.py); it must
    provide randbits(k) and, for transaction rollback, getstate/setstate.
    '''

    def __init__(self, source):
        self._source = source
        self._slots: list = []          # slot -> Fraction weight or None (free)
        self._slot_to_key: list = []    # slot -> key or None
        self._key_to_slot: dict = {}
        self._free: list = []
        self._denom = 1
        self._tree = _Fenwick(1)
        self._version = 0

    # -- introspection -------------------------------------------------

    @property
    def version(self) -> int:
        '''Snapshot version; increments on every committed mutation.'''
        return self._version

    def __len__(self) -> int:
        return len(self._key_to_slot)

    def __contains__(self, key) -> bool:
        return key in self._key_to_slot

    def weight(self, key) -> Fraction:
        return self._slots[self._key_to_slot[key]]

    def total(self) -> Fraction:
        return Fraction(self._tree.total(), self._denom)

    def items(self):
        for key, slot in self._key_to_slot.items():
            yield key, self._slots[slot]

    # -- mutation ------------------------------------------------------

    def add(self, key, weight) -> None:
        w = _as_weight(weight)
        if key in self._key_to_slot:
            raise KeyError(f'duplicate key {key!r}')
        if self._free:
            slot = self._free.pop()
        else:
            slot = len(self._slots)
            self._slots.append(None)
            self._slot_to_key.append(None)
            self._ensure_capacity(slot)
        self._slots[slot] = Fraction(0)
        self._key_to_slot[key] = slot
        self._slot_to_key[slot] = key
        self._set_slot_weight(slot, w)
        self._version += 1

    def update(self, key, weight) -> None:
        w = _as_weight(weight)
        slot = self._key_to_slot[key]
        self._set_slot_weight(slot, w)
        self._version += 1

    def remove(self, key) -> None:
        slot = self._key_to_slot.pop(key)
        self._set_slot_weight(slot, Fraction(0))
        self._slots[slot] = None
        self._slot_to_key[slot] = None
        self._free.append(slot)
        self._version += 1

    # -- sampling ------------------------------------------------------

    def sample(self):
        '''Draw one key with replacement; only the random source advances.'''
        total_int = self._tree.total()
        if total_int <= 0:
            raise EmptySamplerError('total weight is zero')
        r = randbelow(self._source, total_int)
        return self._slot_to_key[self._tree.lower_bound(r)]

    def draw(self):
        '''Draw one key without replacement (auto-committed transaction).'''
        with self.begin() as tx:
            return tx.draw()

    def begin(self) -> 'Transaction':
        '''Open a snapshot-versioned transaction for without-replacement draws.'''
        return Transaction(self)

    # -- internals -----------------------------------------------------

    def _scaled(self, w: Fraction) -> int:
        return w.numerator * (self._denom // w.denominator)

    def _ensure_capacity(self, slot: int) -> None:
        if slot < self._tree.capacity:
            return
        capacity = max(1, self._tree.capacity)
        while slot >= capacity:
            capacity *= 2
        self._rebuild_tree(capacity)

    def _rebuild_tree(self, capacity: int) -> None:
        tree = _Fenwick(capacity)
        for slot, w in enumerate(self._slots):
            if w:
                tree.add(slot, self._scaled(w))
        self._tree = tree

    def _set_slot_weight(self, slot: int, w: Fraction) -> None:
        if self._denom % w.denominator:
            self._denom = lcm(self._denom, w.denominator)
            self._rebuild_tree(self._tree.capacity)
        old = self._slots[slot]
        delta = self._scaled(w) - self._scaled(old)
        if delta:
            self._tree.add(slot, delta)
        self._slots[slot] = w

    def _apply_removals(self, keys) -> None:
        '''Zero the weights of drawn keys; single version bump per commit.'''
        for key in keys:
            slot = self._key_to_slot[key]
            self._set_slot_weight(slot, Fraction(0))
        self._version += 1


class Transaction:
    '''Snapshot of a WeightedSampler for without-replacement batch draws.

    Draws mutate only the private snapshot. commit() applies the removals
    to the live sampler if its version is unchanged since begin();
    rollback() restores the random-source state and discards everything.
    Used as a context manager, it commits on clean exit and rolls back on
    exception.
    '''

    def __init__(self, sampler: WeightedSampler):
        self._sampler = sampler
        self._base_version = sampler._version
        self._rng_state = sampler._source.getstate()
        self._slots = list(sampler._slots)
        self._slot_to_key = list(sampler._slot_to_key)
        self._denom = sampler._denom
        self._tree = _Fenwick(sampler._tree.capacity)
        self._tree.t = list(sampler._tree.t)
        self._drawn: list = []
        self._probability = Fraction(1)
        self._done = False

    def draw(self):
        '''Draw one key without replacement from the snapshot.'''
        self._check_open()
        total_int = self._tree.total()
        if total_int <= 0:
            raise EmptySamplerError('total weight is zero')
        r = randbelow(self._sampler._source, total_int)
        slot = self._tree.lower_bound(r)
        w_int = self._slots[slot].numerator * (self._denom // self._slots[slot].denominator)
        self._probability *= Fraction(w_int, total_int)
        self._tree.add(slot, -w_int)
        self._slots[slot] = Fraction(0)
        key = self._slot_to_key[slot]
        self._drawn.append(key)
        return key

    @property
    def drawn(self) -> tuple:
        '''Keys drawn so far, in order (never repeats).'''
        return tuple(self._drawn)

    def probability(self) -> Fraction:
        '''Exact conditional probability of the sequence drawn so far.'''
        return self._probability

    def commit(self) -> None:
        self._check_open()
        if self._sampler._version != self._base_version:
            raise StaleTransactionError(
                f'sampler changed since begin() '
                f'(base v{self._base_version}, now v{self._sampler._version})'
            )
        self._sampler._apply_removals(self._drawn)
        self._done = True

    def rollback(self) -> None:
        self._check_open()
        self._sampler._source.setstate(self._rng_state)
        self._done = True

    def _check_open(self) -> None:
        if self._done:
            raise RuntimeError('transaction already committed or rolled back')

    def __enter__(self) -> 'Transaction':
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        if self._done:
            return False
        if exc_type is None:
            self.commit()
        else:
            self.rollback()
        return False
