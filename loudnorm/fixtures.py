"""Local deterministic fixtures: sample arrays and sample rate.

No audio devices, no files, no randomness without a fixed seed — every
fixture is reproducible and safe to use from tests and examples.
"""

from __future__ import annotations

import math
import random
from typing import Dict, List, Sequence, Tuple

from .planner import SegmentSpec

SAMPLE_RATE = 48000


def tone(freq: float, seconds: float, amp: float,
         rate: int = SAMPLE_RATE, phase: float = 0.0) -> List[float]:
    n = int(round(seconds * rate))
    return [amp * math.sin(2.0 * math.pi * freq * i / rate + phase)
            for i in range(n)]


def silence(seconds: float, rate: int = SAMPLE_RATE) -> List[float]:
    return [0.0] * int(round(seconds * rate))


def noise(seconds: float, amp: float, seed: int = 20240901,
          rate: int = SAMPLE_RATE) -> List[float]:
    rng = random.Random(seed)
    n = int(round(seconds * rate))
    return [rng.uniform(-amp, amp) for _ in range(n)]


def join(*parts: Sequence[float]) -> List[float]:
    out: List[float] = []
    for part in parts:
        out.extend(part)
    return out


def feasible_case() -> Tuple[List[float], int, List[SegmentSpec], Dict]:
    """Two tones whose targets are jointly reachable."""
    a = tone(220.0, 0.5, 0.1)
    b = tone(330.0, 0.5, 0.5)
    samples = join(a, b)
    specs = [
        SegmentSpec(0, len(a), 0.2),
        SegmentSpec(len(a), len(samples), 0.2),
    ]
    kwargs = dict(peak_ceiling=0.9, max_gain_step=5.0, fade_len=256)
    return samples, SAMPLE_RATE, specs, kwargs


def infeasible_case() -> Tuple[List[float], int, List[SegmentSpec], Dict]:
    """Adjacent targets too far apart for the strict rate limit."""
    a = tone(200.0, 0.4, 0.5)
    b = tone(200.0, 0.4, 0.01)
    samples = join(a, b)
    specs = [
        SegmentSpec(0, len(a), 0.05),
        SegmentSpec(len(a), len(samples), 0.5),
    ]
    kwargs = dict(peak_ceiling=10.0, max_gain_step=0.2, fade_len=64)
    return samples, SAMPLE_RATE, specs, kwargs


def crossfade_peak_case() -> Tuple[List[float], int, List[SegmentSpec], Dict]:
    """High-gain quiet noise next to a loud tone: the crossfade between
    them peaks above either segment's own scaled peak."""
    a = noise(0.4, 0.05, seed=7)
    b = tone(440.0, 0.4, 0.9)
    samples = join(a, b)
    specs = [
        SegmentSpec(0, len(a), 0.23),
        SegmentSpec(len(a), len(samples), 0.30),
    ]
    kwargs = dict(peak_ceiling=1.0, max_gain_step=100.0, fade_len=512)
    return samples, SAMPLE_RATE, specs, kwargs


def silence_case() -> Tuple[List[float], int, List[SegmentSpec], Dict]:
    """Leading silence must stay silent even with a high target."""
    a = silence(0.25)
    b = tone(500.0, 0.5, 0.2)
    samples = join(a, b)
    specs = [
        SegmentSpec(0, len(a), 0.5),
        SegmentSpec(len(a), len(samples), 0.3),
    ]
    kwargs = dict(peak_ceiling=0.9, max_gain_step=5.0, fade_len=128)
    return samples, SAMPLE_RATE, specs, kwargs


def short_segment_case() -> Tuple[List[float], int, List[SegmentSpec], Dict]:
    """A 37-sample segment whose true energy is not amp/sqrt(2)."""
    a = silence(0.002)
    b = tone(2000.0, 37.0 / SAMPLE_RATE, 0.1)[:37]
    c = silence(0.002)
    samples = join(a, b, c)
    specs = [
        SegmentSpec(0, len(a), 0.0),
        SegmentSpec(len(a), len(a) + len(b), 0.05),
        SegmentSpec(len(a) + len(b), len(samples), 0.0),
    ]
    kwargs = dict(peak_ceiling=0.9, max_gain_step=10.0, fade_len=8)
    return samples, SAMPLE_RATE, specs, kwargs
