"""Local deterministic fixtures: sample arrays and sample rates are generated
here; no audio device is ever touched."""
from __future__ import annotations

import math
import random

SAMPLE_RATE_A = 16000
SAMPLE_RATE_B = 8000


def tone(freq, n, sr=SAMPLE_RATE_A, amp=0.6, phase=0.0):
    return [amp * math.sin(2.0 * math.pi * freq * k / sr + phase) for k in range(n)]


def noise(n, seed=20241001, amp=0.15):
    rng = random.Random(seed)
    return [amp * (rng.random() * 2.0 - 1.0) for _ in range(n)]


def mixed(n, sr=SAMPLE_RATE_A, seed=7):
    t1 = tone(440.0, n, sr, 0.5)
    t2 = tone(1234.5, n, sr, 0.25, 0.7)
    nz = noise(n, seed, 0.05)
    return [a + b + c for a, b, c in zip(t1, t2, nz)]
