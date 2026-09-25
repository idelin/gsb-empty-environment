"""Local fixtures: sample arrays, sample rate, declared latency, tolerance.

No audio device is involved. Source material is deterministic so every
scenario is exactly replayable.
"""
from __future__ import annotations

import math

SAMPLE_RATE = 48_000
BLOCK_SIZE = 128

#: Declared scheduling latency: exactly one render block.
DECLARED_LATENCY_SECONDS = BLOCK_SIZE / SAMPLE_RATE  # ~2.667 ms

#: Numeric tolerance used when cross-checking against the reference renderer.
TOLERANCE = 1e-9

SOURCE_LENGTH = 4096

TRACKS = ["a", "b", "c"]
MASTER = "master"


def make_sources(length: int = SOURCE_LENGTH, sample_rate: int = SAMPLE_RATE):
    """Deterministic per-track source sample arrays (index = absolute sample)."""
    a = [0.5 * math.sin(2.0 * math.pi * 440.0 * i / sample_rate) for i in range(length)]
    b = [((i * 7) % 97) / 97.0 - 0.5 for i in range(length)]
    c = [0.25 if (i // 32) % 2 == 0 else -0.25 for i in range(length)]
    return {"a": a, "b": b, "c": c}
