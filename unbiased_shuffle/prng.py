"""Explicit, serializable pseudo-random sources (caller-injected).

A random source only has to provide ``getrandbits(k)``; the unbiased
``randbelow(n)`` is mixed in from :mod:`unbiased_shuffle.rangesample`.
Sources also expose ``getstate()`` / ``setstate()`` so checkpoints can
capture and restore the exact random stream position.

``SplitMix64Source`` is the reference implementation: the whole state is
two integers (``state``, ``draws``) plus the recorded ``seed``, which makes
it trivial to serialize into JSON checkpoints.  ``draws`` counts the number
of 64-bit words produced, i.e. the raw sample size consumed from the source.
"""

from __future__ import annotations

import random

from . import rangesample

_MASK64 = (1 << 64) - 1
_GAMMA = 0x9E3779B97F4A7C15
_MULT1 = 0xBF58476D1CE4E5B9
_MULT2 = 0x94D049BB133111EB


class SplitMix64Source:
    """Explicit SplitMix64 PRNG with a fully serializable state."""

    def __init__(self, seed: int, state: int | None = None, draws: int = 0):
        self.seed = seed
        self.state = (seed if state is None else state) & _MASK64
        self.draws = draws  # number of 64-bit words produced so far

    def next_word(self) -> int:
        """Advance the generator and return one uniform 64-bit word."""
        self.state = (self.state + _GAMMA) & _MASK64
        z = self.state
        z = ((z ^ (z >> 30)) * _MULT1) & _MASK64
        z = ((z ^ (z >> 27)) * _MULT2) & _MASK64
        z ^= z >> 31
        self.draws += 1
        return z

    def getrandbits(self, k: int) -> int:
        """Return a uniform integer in [0, 2**k); supports any k >= 0."""
        if k <= 0:
            return 0
        value = 0
        shift = 0
        while shift < k:
            value |= self.next_word() << shift
            shift += 64
        return value & ((1 << k) - 1)

    def randbelow(self, n: int) -> int:
        return rangesample.randbelow(self, n)

    def getstate(self) -> dict:
        return {"seed": self.seed, "state": self.state, "draws": self.draws}

    def setstate(self, state: dict) -> None:
        self.seed = state["seed"]
        self.state = state["state"] & _MASK64
        self.draws = state["draws"]


class RandomModuleSource:
    """Adapter that injects a ``random.Random`` instance as the source.

    Useful when a caller prefers Mersenne Twister; the interface (and the
    unbiased ``randbelow``) is identical.  The state is stored as a pickle
    hex string so it still fits into JSON checkpoints.
    """

    def __init__(self, seed: int):
        import pickle

        self._pickle = pickle
        self.seed = seed
        self._rng = random.Random(seed)
        self.draws = 0

    def getrandbits(self, k: int) -> int:
        if k <= 0:
            return 0
        self.draws += 1
        return self._rng.getrandbits(k)

    def randbelow(self, n: int) -> int:
        return rangesample.randbelow(self, n)

    def getstate(self) -> dict:
        return {
            "seed": self.seed,
            "state": self._pickle.dumps(self._rng.getstate()).hex(),
            "draws": self.draws,
        }

    def setstate(self, state: dict) -> None:
        self.seed = state["seed"]
        self._rng.setstate(self._pickle.loads(bytes.fromhex(state["state"])))
        self.draws = state["draws"]
