"""Deterministic local fixtures: (float32 sample array, sample rate) pairs.

No audio devices, files, or network are involved; signals are synthesized
with the standard library only.
"""
from __future__ import annotations

import math
import random
from array import array

SAMPLE_RATE = 8000


def _sine(amp: float, freq: float, count: int, sr: int, phase: float = 0.0):
    return [amp * math.sin(2.0 * math.pi * freq * k / sr + phase)
            for k in range(count)]


def loud_quiet_tone(sr: int = SAMPLE_RATE):
    """Quiet 440 Hz tone followed by a loud one (no overlap)."""
    samples = _sine(0.05, 440.0, 4000, sr) + _sine(0.5, 440.0, 4000, sr)
    return array("f", samples), sr


def tone_with_silence(sr: int = SAMPLE_RATE):
    """Tone, then digital silence, then tone again."""
    samples = (_sine(0.3, 330.0, 2000, sr) + [0.0] * 2000
               + _sine(0.3, 330.0, 2000, sr))
    return array("f", samples), sr


def short_blip(sr: int = SAMPLE_RATE):
    """Low tone with a 4-sample high-level blip at 1500:1504."""
    samples = _sine(0.02, 220.0, 3000, sr)
    samples[1500:1504] = [0.4, -0.4, 0.4, -0.4]
    return array("f", samples), sr


def overlap_peak_tone(sr: int = SAMPLE_RATE):
    """Steady 0.9-amplitude tone used to stress crossfade peak checks."""
    return array("f", _sine(0.9, 440.0, 4500, sr)), sr


def noisy_segments(sr: int = SAMPLE_RATE, seed: int = 20260926):
    """Two noise regions of different level, deterministically seeded."""
    rng = random.Random(seed)
    samples = ([rng.uniform(-0.2, 0.2) for _ in range(2000)]
               + [rng.uniform(-0.7, 0.7) for _ in range(2000)])
    return array("f", samples), sr
