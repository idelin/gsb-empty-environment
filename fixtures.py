"""Local fixtures: deterministic sample arrays and a sample rate.

No audio device is involved.  Tracks are generated from closed-form
functions of the sample index plus a seeded PRNG, so every run (and every
test) sees exactly the same data.  Missing samples are represented by
``None`` to exercise the multi-track missing-data mask.
"""
from __future__ import annotations

import math
import random
from typing import List, Optional, Tuple

SAMPLE_RATE = 8000  # Hz, declared by the fixture
NUM_SAMPLES = 400   # source length per track


def _tone(index: int, freq: float, phase: float) -> float:
    return math.sin(2.0 * math.pi * freq * index / SAMPLE_RATE + phase)


def load_tracks() -> Tuple[List[List[Optional[float]]], int]:
    """Return ``(tracks, sample_rate)`` with deterministic content."""
    rng = random.Random(20260926)
    tracks: List[List[Optional[float]]] = []
    for t in range(3):
        freq = 40.0 * (t + 1)
        phase = 0.5 * t
        track: List[Optional[float]] = [
            _tone(i, freq, phase) + 0.1 * (rng.random() - 0.5)
            for i in range(NUM_SAMPLES)
        ]
        tracks.append(track)
    # Missing-data holes, at different places per track.
    for i in range(50, 60):
        tracks[0][i] = None
    for i in range(120, 127):
        tracks[1][i] = None
    for i in range(300, 312):
        tracks[2][i] = None
    return tracks, SAMPLE_RATE
