"""Local fixtures: deterministic sample arrays and the sample rate.

No audio device is ever opened; everything is synthesized here with a
seeded PRNG so tests and replays are fully reproducible.
"""

from __future__ import annotations

import random

#: Declared acquisition parameters (fixture-only, no hardware involved).
SAMPLE_RATE = 48_000
FRAME_HOP = 512
BANDS = ("low", "mid", "high")
SCENES = ("quiet_room", "street")

#: Frames that carry injected extreme peaks per band (fixture ground truth).
PEAK_FRAMES = {"low": (30, 31), "mid": (45,), "high": ()}


def band_values(band: str, n_frames: int, seed: int = 7) -> list[float]:
    """Deterministic per-band magnitude series with injected peaks."""
    base = {"low": 12.0, "mid": 8.0, "high": 5.0}[band]
    rng = random.Random(f"{seed}:{band}")
    values = []
    for frame in range(n_frames):
        value = base + rng.uniform(-1.5, 1.5)
        if frame in PEAK_FRAMES[band]:
            value += 100.0  # extreme transient peak
        values.append(value)
    return values


def frame_to_seconds(frame: int) -> float:
    return frame * FRAME_HOP / SAMPLE_RATE
