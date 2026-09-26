"""Synthetic local->reference offset conversion table (deterministic).

The 'true' reference clock runs at a piecewise-constant drift against the
local clock; measurements add bounded noise plus a few gross outliers.
Everything is exact Fraction arithmetic.
"""

from __future__ import annotations

from fractions import Fraction
from random import Random

from .piecewise import Anchor
from .timebase import Duration, Instant

# True offset (ref - local): 5 s base, drifting +2 ms/s up to t = 300 s,
# then -1 ms/s afterwards (continuous at the knee).
_KNEE = Fraction(300)
_DRIFT1 = Fraction(2, 1000)
_DRIFT2 = Fraction(-1, 1000)
_BASE = Fraction(5)


def true_offset(local_seconds):
    t = Fraction(local_seconds)
    if t <= _KNEE:
        return _BASE + _DRIFT1 * t
    return _BASE + _DRIFT1 * _KNEE + _DRIFT2 * (t - _KNEE)


def true_ref(local_seconds):
    return Fraction(local_seconds) + true_offset(local_seconds)


def synthetic_table(count=12, step_s=60, noise_ms=50, outliers=None, seed=7):
    """Anchors at `step_s` spacing; noise within +/-noise_ms; gross outliers.

    `outliers` maps anchor index -> exact ref shift in seconds.
    """
    if outliers is None:
        outliers = {3: Fraction(2), 8: Fraction(-3, 2)}
    rng = Random(seed)
    anchors = []
    for i in range(count):
        local = Fraction(i * step_s)
        noise = Fraction(rng.randint(-noise_ms, noise_ms), 1000)
        ref = true_ref(local) + noise + outliers.get(i, Fraction(0))
        anchors.append(
            Anchor(Instant(local), Instant(ref), Duration(milliseconds=noise_ms))
        )
    return anchors
