"""Deterministic local fixtures: sample arrays + sampling rate, no audio device."""

from __future__ import annotations

import random
from dataclasses import dataclass

from .model import MISSING, VALID

SAMPLE_RATE = 16000
HOP = 256
FRAME_SECONDS = HOP / SAMPLE_RATE  # 16 ms per frame (declared latency grain)

SEED = 20260926
BANDS = ("low", "mid", "high")
SCENES = ("office", "street")

BASE_LEVEL = {
    ("office", "low"): 0.90, ("office", "mid"): 0.60, ("office", "high"): 0.30,
    ("street", "low"): 1.40, ("street", "mid"): 1.10, ("street", "high"): 0.70,
}

PEAK_FRAME = 40
PEAK_BAND = "mid"
PEAK_VALUE = 60.0
MISSING_FRAMES = frozenset({17, 33})  # band "high" only


@dataclass(frozen=True)
class FixtureSample:
    frame: int
    scene: str
    band: str
    value: float | None
    status: str


def scene_of(frame, n_frames):
    return SCENES[0] if frame < n_frames // 2 else SCENES[1]


def make_stream(n_frames=96, seed=SEED):
    """Deterministic sample stream; frames 0..n_frames-1 across all bands."""
    rng = random.Random(seed)
    out = []
    for f in range(n_frames):
        scene = scene_of(f, n_frames)
        for band in BANDS:
            if band == "high" and f in MISSING_FRAMES:
                out.append(FixtureSample(f, scene, band, None, MISSING))
                continue
            base = BASE_LEVEL[(scene, band)]
            value = round(base * (1.0 + 0.04 * rng.gauss(0.0, 1.0)), 6)
            if f == PEAK_FRAME and band == PEAK_BAND:
                value = PEAK_VALUE
            out.append(FixtureSample(f, scene, band, value, VALID))
    return out
