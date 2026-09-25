"""Minimal deterministic DSP primitives (standard library only)."""

from __future__ import annotations

import math


class OnePoleLowpass:
    """One-pole lowpass filter with persistent state across blocks.

    ``cutoff is None`` means bypass (passthrough).  State ``z`` is carried
    across block boundaries so block-size changes never affect the output.
    """

    def __init__(self, sample_rate: int, cutoff: float | None = None):
        self.sample_rate = sample_rate
        self.z = 0.0
        self.cutoff: float | None = None
        self.alpha = 1.0
        self.set_cutoff(cutoff)

    def set_cutoff(self, cutoff: float | None) -> None:
        if cutoff is not None:
            if not (0.0 < cutoff < self.sample_rate / 2.0):
                raise ValueError(
                    f"cutoff {cutoff!r} outside (0, {self.sample_rate / 2.0})")
            self.alpha = 1.0 - math.exp(-2.0 * math.pi * cutoff
                                        / self.sample_rate)
        else:
            self.alpha = 1.0
        self.cutoff = cutoff

    def process(self, x: float) -> float:
        if self.cutoff is None:
            return x
        self.z += self.alpha * (x - self.z)
        return self.z
