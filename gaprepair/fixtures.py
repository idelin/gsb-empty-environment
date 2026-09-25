"""Deterministic local fixtures: sample arrays and the sample rate.

No audio device is ever opened; every signal is synthesized here with a
fixed seed so runs are reproducible.
"""

from __future__ import annotations

import math
import random

SAMPLE_RATE = 8000
SEED = 20260926


def load_clean_fixture():
    """Return ``(samples, sample_rate)`` for a fully known signal.

    Two exact ``0.0`` values are included on purpose: real zeros are valid
    observations and must never be treated as missing.
    """
    rng = random.Random(SEED)
    n = 400
    samples = []
    for i in range(n):
        t = i / SAMPLE_RATE
        value = (
            0.6 * math.sin(2.0 * math.pi * 220.0 * t)
            + 0.3 * math.sin(2.0 * math.pi * 440.0 * t + 0.5)
            + 0.4 * math.sin(2.0 * math.pi * 55.0 * t + 1.0)
            + 0.02 * rng.gauss(0.0, 1.0)
        )
        samples.append(value)
    samples[100] = 0.0
    samples[250] = 0.0
    return samples, SAMPLE_RATE


def load_fixture_with_gaps():
    """Return ``(samples, sample_rate, gap_specs)`` with NaN gaps inserted."""
    samples, rate = load_clean_fixture()
    gap_specs = [(40, 3), (120, 6), (200, 12), (300, 4)]
    occluded = list(samples)
    for start, length in gap_specs:
        for i in range(start, start + length):
            occluded[i] = float("nan")
    return occluded, rate, gap_specs


def load_fixture_with_edge_gaps():
    """Fixture with a leading gap (no left anchor) and a trailing gap."""
    samples, rate = load_clean_fixture()
    occluded = list(samples)
    for i in range(0, 4):
        occluded[i] = float("nan")
    for i in range(len(occluded) - 3, len(occluded)):
        occluded[i] = float("nan")
    return occluded, rate, [(0, 4), (len(occluded) - 3, 3)]


def calibration_occlusions(count=24, length=4, seed=7):
    """Deterministic, non-overlapping occlusion windows for calibration."""
    n = 400
    rng = random.Random(seed)
    occlusions = []
    while len(occlusions) < count:
        start = rng.randrange(32, n - length - 32)
        if all(abs(start - s) > 2 * length for s, _ in occlusions):
            occlusions.append((start, length))
    return sorted(occlusions)
