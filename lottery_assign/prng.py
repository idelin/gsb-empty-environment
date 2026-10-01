"""Fixed-definition integer PRNG (SplitMix64).

The algorithm is defined explicitly here so that results are reproducible
across processes, machines and Python restarts.  No runtime randomness,
time sources or hash ordering are involved anywhere in this package.
"""

MASK64 = (1 << 64) - 1

# SplitMix64 constants (public domain, Steele/Lea/Flood).
_GAMMA = 0x9E3779B97F4A7C15
_MIX1 = 0xBF58476D1CE4E5B9
_MIX2 = 0x94D049BB133111EB


class SplitMix64:
    """Deterministic 64-bit PRNG seeded by a single unsigned 64-bit integer."""

    def __init__(self, seed: int):
        if not isinstance(seed, int):
            raise TypeError("seed must be an int")
        self._state = seed & MASK64

    def next_u64(self) -> int:
        """Advance the state and return the next unsigned 64-bit value."""
        self._state = (self._state + _GAMMA) & MASK64
        z = self._state
        z = ((z ^ (z >> 30)) * _MIX1) & MASK64
        z = ((z ^ (z >> 27)) * _MIX2) & MASK64
        return z ^ (z >> 31)
