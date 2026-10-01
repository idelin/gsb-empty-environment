"""Injectable random sources with exact uniform integer generation.

All sources implement the same small interface:

    randbelow(n) -> int   uniform integer in [0, n)
    getstate() / setstate(state)   for transaction rollback

Uniformity is achieved by *rejection sampling*, never by modulo:
draw k = n.bit_length() fresh bits, reject values >= n and redraw.
For each j in [0, n):

    P(result = j) = sum_{t>=0} (1 - n/2^k)^t * 2^-k
                  = 2^-k / (1 - (1 - n/2^k))
                  = 1 / n          (exact, not approximate)
"""

from __future__ import annotations

import random
from typing import Sequence


class BitsExhaustedError(RuntimeError):
    """A finite bit-stream source ran out of bits.

    Raised only by BitStreamSource / ScriptedSource; the exact-enumeration
    tests catch it to account for the (exactly computable) probability mass
    of bit strings that do not terminate within the enumerated length.
    """


def rejection_randbelow(getbits, n: int) -> int:
    """Uniform integer in [0, n) via rejection sampling (no modulo bias)."""
    if n <= 0:
        raise ValueError("n must be a positive integer")
    k = n.bit_length()
    while True:
        x = getbits(k)
        if x < n:
            return x


class SeededRandomSource:
    """Caller-injected seeded source wrapping random.Random.

    Records the seed and consumption statistics (bits consumed, values
    returned) so every experiment is reproducible and auditable.
    """

    def __init__(self, seed: int):
        self.seed = seed
        self._rng = random.Random(seed)
        self.bits_consumed = 0
        self.values_returned = 0

    def _getbits(self, k: int) -> int:
        self.bits_consumed += k
        return self._rng.getrandbits(k)

    def randbelow(self, n: int) -> int:
        value = rejection_randbelow(self._getbits, n)
        self.values_returned += 1
        return value

    def getstate(self):
        return (self._rng.getstate(), self.bits_consumed, self.values_returned)

    def setstate(self, state) -> None:
        rng_state, self.bits_consumed, self.values_returned = state
        self._rng.setstate(rng_state)


class BitStreamSource:
    """Deterministic source reading k-bit chunks (MSB first) from a fixed
    bit string of the given length, represented as an integer.

    Used by the exact-enumeration tests to run the sampler over *all*
    bit strings of a given length. Raises BitsExhaustedError when the
    string is consumed before rejection sampling accepts.
    """

    def __init__(self, value: int, length: int):
        if not 0 <= value < (1 << length):
            raise ValueError("value does not fit in length bits")
        self._value = value
        self._length = length
        self._pos = 0
        self.values_returned = 0

    def _getbits(self, k: int) -> int:
        if self._pos + k > self._length:
            raise BitsExhaustedError(
                f"bit stream exhausted after {self._pos} of {self._length} bits"
            )
        shift = self._length - self._pos - k
        x = (self._value >> shift) & ((1 << k) - 1)
        self._pos += k
        return x

    def randbelow(self, n: int) -> int:
        value = rejection_randbelow(self._getbits, n)
        self.values_returned += 1
        return value

    def getstate(self):
        return (self._pos, self.values_returned)

    def setstate(self, state) -> None:
        self._pos, self.values_returned = state


class ScriptedSource:
    """Returns caller-specified integers; each must satisfy 0 <= v < n.

    Used for manual recomputation tests with fixed random numbers.
    """

    def __init__(self, values: Sequence[int]):
        self._values = list(values)
        self._pos = 0
        self.values_returned = 0

    def randbelow(self, n: int) -> int:
        if self._pos >= len(self._values):
            raise BitsExhaustedError("scripted values exhausted")
        v = self._values[self._pos]
        if not 0 <= v < n:
            raise ValueError(f"scripted value {v} out of range [0, {n})")
        self._pos += 1
        self.values_returned += 1
        return v

    def getstate(self):
        return (self._pos, self.values_returned)

    def setstate(self, state) -> None:
        self._pos, self.values_returned = state
