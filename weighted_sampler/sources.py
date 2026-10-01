'''Caller-injected random sources and bias-free integer sampling.

A random source only needs to provide randbits(k) -> int (k uniform
random bits). For transaction rollback to restore the random state, the
source must also provide getstate() / setstate(state).

randbelow uses rejection sampling: draw k = bit_length(n - 1) bits and
reject values >= n. Every integer in [0, n) therefore has probability
exactly 1/n -- there is no modulo bias.
'''

from __future__ import annotations

import random


class ExhaustedBitsError(Exception):
    '''Raised by BitSource when its fixed bit stream runs out.'''


class SeededRandomSource:
    '''Random source backed by random.Random; the seed is recorded.'''

    def __init__(self, seed: int):
        self.seed = seed
        self._rng = random.Random(seed)

    def randbits(self, k: int) -> int:
        return self._rng.getrandbits(k)

    def getstate(self) -> object:
        return self._rng.getstate()

    def setstate(self, state: object) -> None:
        self._rng.setstate(state)


class BitSource:
    '''Deterministic source consuming a fixed sequence of bits (MSB first).

    Used by tests to enumerate the full bit space (exact distribution
    proofs) and to make fixed random numbers manually recomputable.
    Supports getstate/setstate, so transactions can roll it back.
    '''

    def __init__(self, bits):
        self._bits = [int(b) for b in bits]
        if any(b not in (0, 1) for b in self._bits):
            raise ValueError('bits must be 0 or 1')
        self._pos = 0

    @classmethod
    def from_int(cls, value: int, width: int) -> 'BitSource':
        return cls([(value >> (width - 1 - i)) & 1 for i in range(width)])

    def randbits(self, k: int) -> int:
        if self._pos + k > len(self._bits):
            raise ExhaustedBitsError(
                f'need {k} bits at position {self._pos}, only '
                f'{len(self._bits) - self._pos} left'
            )
        value = 0
        for _ in range(k):
            value = (value << 1) | self._bits[self._pos]
            self._pos += 1
        return value

    @property
    def consumed(self) -> int:
        return self._pos

    def getstate(self) -> int:
        return self._pos

    def setstate(self, state: int) -> None:
        self._pos = state


def randbelow(source, n: int) -> int:
    '''Uniform integer in [0, n) via rejection sampling (no modulo bias).'''
    if n <= 0:
        raise ValueError('randbelow requires n >= 1')
    k = (n - 1).bit_length()
    while True:
        r = source.randbits(k)
        if r < n:
            return r
