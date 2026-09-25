"""Local, deterministic test fixtures.

No audio devices, files or network access: every sample array is synthesised
in-process from a fixed seed.
"""

from __future__ import annotations

import math
import random
from fractions import Fraction
from typing import List, Tuple

DEFAULT_RATE = Fraction(44100)
ALT_RATE = Fraction(48000)


def waveform(n: int = 257, seed: int = 1234) -> List[float]:
    """Low-amplitude waveform with a known sharp spike and missing gaps.

    Most samples sit in [-2, 2]; a single +9.0 spike is inserted so that no
    average can mask it.  Two gaps are marked with NaN (missing data).
    """
    rng = random.Random(seed)
    data: List[float] = []
    for i in range(n):
        data.append(
            math.sin(i / 7.0)
            + 0.25 * math.cos(i / 3.0)
            + rng.uniform(-0.1, 0.1)
        )
    spike_at = n // 3
    data[spike_at] = 9.0
    for gap in (spike_at + 5, n - 11):
        if 0 <= gap < n:
            data[gap] = math.nan
    return data


def tied_waveform(n: int = 64) -> List[float]:
    """Several equal maxima, including an adjacent tied pair."""
    data = [math.sin(i / 5.0) * 0.3 for i in range(n)]
    for i in (3, 17, 40):
        data[i] = 5.0
    data[41] = 5.0
    return data


def rates() -> Tuple[Fraction, Fraction]:
    return DEFAULT_RATE, ALT_RATE
