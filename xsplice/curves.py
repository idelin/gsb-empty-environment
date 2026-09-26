"""Fade curve shapes.

A fade of length N covers local sample indices 0 .. N-1.  The normalized
position of local index ``i`` is ``x = i / N``, so a fade-in starts at gain
exactly 0.0 and reaches 1.0 on the first sample *after* the fade region
(symmetrically, a fade-out ends at 0.0 on the first sample after its
region).  With this convention a matched pair of fades -- one segment
fading out while another fades in over the same output samples -- forms a
constant-amplitude (linear) or constant-power (equal-power) crossfade at
every sample of the overlap, and the overlap is counted exactly once on
the output timeline.
"""

from __future__ import annotations

import math

LINEAR = "linear"
EQUAL_POWER = "equal_power"
CURVES = (LINEAR, EQUAL_POWER)


def fade_in_gain(curve: str, x: float) -> float:
    """Fade-in gain at normalized position ``x`` in [0, 1]."""
    if curve == LINEAR:
        return x
    if curve == EQUAL_POWER:
        return math.sin(0.5 * math.pi * x)
    raise ValueError(f"unknown curve: {curve!r}")


def fade_out_gain(curve: str, x: float) -> float:
    """Fade-out gain at normalized position ``x`` in [0, 1]."""
    return fade_in_gain(curve, 1.0 - x)
