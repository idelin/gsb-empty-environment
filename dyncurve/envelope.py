"""Envelope detection and gain smoothing.

These are the *stateful* parts of a dynamics processor.  They are kept
strictly separate from the static CompiledCurve, which stays a pure
function of level.  Coefficients: c = exp(-1 / (time_s * fs)); a zero
time constant means "instant" (c = 0).
"""
from __future__ import annotations

import math


def _coef(time_s: float, sample_rate: float) -> float:
    if time_s <= 0.0:
        return 0.0
    return math.exp(-1.0 / (time_s * sample_rate))


class PeakDetector:
    """Attack/release smoothed |x|."""

    def __init__(self, attack_s: float, release_s: float, sample_rate: float):
        self._att = _coef(attack_s, sample_rate)
        self._rel = _coef(release_s, sample_rate)
        self.state = 0.0

    def next(self, x: float) -> float:
        v = abs(x)
        c = self._att if v > self.state else self._rel
        self.state = c * self.state + (1.0 - c) * v
        return self.state

    def get_state(self) -> float:
        return self.state

    def set_state(self, state: float) -> None:
        self.state = float(state)


class RMSDetector:
    """Attack/release smoothed mean-square, returned as RMS."""

    def __init__(self, attack_s: float, release_s: float, sample_rate: float):
        self._att = _coef(attack_s, sample_rate)
        self._rel = _coef(release_s, sample_rate)
        self.state = 0.0  # mean-square state

    def next(self, x: float) -> float:
        v = x * x
        c = self._att if v > self.state else self._rel
        self.state = c * self.state + (1.0 - c) * v
        return math.sqrt(self.state)

    def get_state(self) -> float:
        return self.state

    def set_state(self, state: float) -> None:
        self.state = float(state)


class GainSmoother:
    """Attack/release smoothing of a gain trajectory in dB.

    "Attack" applies when the gain is decreasing (more reduction),
    "release" when it is recovering.
    """

    def __init__(self, attack_s: float, release_s: float, sample_rate: float):
        self._att = _coef(attack_s, sample_rate)
        self._rel = _coef(release_s, sample_rate)
        self.state = 0.0  # current smoothed gain in dB

    def next(self, target_db: float) -> float:
        c = self._att if target_db < self.state else self._rel
        self.state = c * self.state + (1.0 - c) * target_db
        return self.state

    def get_state(self) -> float:
        return self.state

    def set_state(self, state: float) -> None:
        self.state = float(state)
