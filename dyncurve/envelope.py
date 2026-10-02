"""Envelope detection and gain smoothing.

Deliberately separated from the static curve (dyncurve.curve): detectors
turn sample magnitudes into a level estimate, the static curve maps level to
a target gain, and the gain smoother low-passes the target gain in dB. The
three stages are independent and individually testable.

All filters are causal one-poles: they add zero latency.
"""
from __future__ import annotations

import math
from typing import List

from .curve import DB_FLOOR


def _coef(time_ms: float, sample_rate: float) -> float:
    """One-pole coefficient for a time constant in milliseconds."""
    if time_ms <= 0.0:
        return 0.0
    return math.exp(-1.0 / (time_ms * 1e-3 * sample_rate))


def amplitude_to_db(amp: float) -> float:
    """Level in dBFS, clamped to DB_FLOOR so silence never yields -inf."""
    if amp <= 0.0:
        return DB_FLOOR
    return max(20.0 * math.log10(amp), DB_FLOOR)


class PeakDetector:
    """Attack/release peak envelope follower on |x|."""

    def __init__(self, sample_rate: float, attack_ms: float, release_ms: float):
        self.attack_coef = _coef(attack_ms, sample_rate)
        self.release_coef = _coef(release_ms, sample_rate)
        self.env = 0.0

    def update(self, x: float) -> float:
        coef = self.attack_coef if x > self.env else self.release_coef
        self.env = coef * self.env + (1.0 - coef) * x
        return self.env

    def get_state(self) -> float:
        return self.env

    def set_state(self, state: float) -> None:
        self.env = float(state)


class RmsDetector:
    """One-pole mean-square detector; envelope is sqrt of the mean square."""

    def __init__(self, sample_rate: float, window_ms: float):
        self.coef = _coef(window_ms, sample_rate)
        self.mean_sq = 0.0

    def update(self, x: float) -> float:
        self.mean_sq = self.coef * self.mean_sq + (1.0 - self.coef) * x * x
        return math.sqrt(max(self.mean_sq, 0.0))

    def get_state(self) -> float:
        return self.mean_sq

    def set_state(self, state: float) -> None:
        self.mean_sq = float(state)


class GainSmoother:
    """Smooths a target gain (dB) with attack/release one-pole behaviour.

    'attack' applies when the target is more negative than the current state
    (gain reduction), 'release' when it recovers. Works in the dB domain.
    """

    def __init__(self, sample_rate: float, attack_ms: float, release_ms: float):
        self.attack_coef = _coef(attack_ms, sample_rate)
        self.release_coef = _coef(release_ms, sample_rate)
        self.gain_db = 0.0

    def update(self, target_db: float) -> float:
        coef = self.attack_coef if target_db < self.gain_db else self.release_coef
        self.gain_db = coef * self.gain_db + (1.0 - coef) * target_db
        return self.gain_db

    def get_state(self) -> float:
        return self.gain_db

    def set_state(self, state: float) -> None:
        self.gain_db = float(state)


def make_detector(kind: str, sample_rate: float, attack_ms: float,
                  release_ms: float, window_ms: float):
    if kind == "peak":
        return PeakDetector(sample_rate, attack_ms, release_ms)
    if kind == "rms":
        return RmsDetector(sample_rate, window_ms)
    raise ValueError(f"unknown detector kind: {kind!r}")


def detector_states(detectors) -> List[float]:
    return [d.get_state() for d in detectors]


def restore_detector_states(detectors, states) -> None:
    for d, s in zip(detectors, states):
        d.set_state(s)
