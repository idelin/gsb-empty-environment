"""Local fixtures: deterministic sample arrays and sample rates.

No audio device is involved; every sample array is generated in-process.
Rates are intentionally tiny (2/4/8 Hz) so exact Fraction times stay
human-readable in tests and in the demo.
"""

from __future__ import annotations

from fractions import Fraction
from typing import List, Tuple

from .core import SampleBlock


def ramp(start: float, step: float, n: int) -> Tuple[float, ...]:
    """A deterministic ramp of ``n`` samples: start, start+step, ..."""
    return tuple(start + step * i for i in range(n))


def make_blocks() -> List[SampleBlock]:
    """Fixture timeline exercising rate switch, a gap, and a versioned overlap.

    * alpha: 4 Hz, 8 samples, v1, base 0      -> covers [0, 2)
    * gamma: 4 Hz, 4 samples, v2, base 3/2    -> covers [3/2, 5/2), wins over alpha
    * beta:  2 Hz, 3 samples, v1, base 3      -> covers [3, 9/2); hole (5/2, 3)
    * delta: 8 Hz, 8 samples, v1, base 9/2    -> covers [9/2, 11/2); rate switch 2 -> 8
    """
    return [
        SampleBlock("alpha", ramp(0.0, 1.0, 8), sample_rate=4, version=1,
                    base_time=Fraction(0)),
        SampleBlock("gamma", ramp(20.0, 1.0, 4), sample_rate=4, version=2,
                    base_time=Fraction(3, 2)),
        SampleBlock("beta", ramp(10.0, 1.0, 3), sample_rate=2, version=1,
                    base_time=Fraction(3)),
        SampleBlock("delta", ramp(30.0, 1.0, 8), sample_rate=8, version=1,
                    base_time=Fraction(9, 2)),
    ]


def calibration_shift_alpha() -> Fraction:
    """Offset used by the demo/tests to shift the historical block ``alpha``."""
    return Fraction(1, 4)
