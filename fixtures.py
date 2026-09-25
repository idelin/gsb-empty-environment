"""Local fixtures: deterministic sample arrays and sample rates.

No audio device is involved; everything is generated in-process.
"""

import math
import random
from fractions import Fraction

RATES = {
    "voice": Fraction(16000),
    "cd": Fraction(44100),
    "studio": Fraction(48000),
}


def sine_wave(n, freq=440.0, rate=RATES["cd"], amp=0.8):
    r = float(rate)
    return [amp * math.sin(2.0 * math.pi * freq * i / r) for i in range(n)]


def noise(n, seed=1234, amp=1.0):
    rng = random.Random(seed)
    return [rng.uniform(-amp, amp) for _ in range(n)]


def impulse(n, at, height=10.0, base=0.0):
    out = [base] * n
    out[at] = height
    return out


def with_dropouts(samples, positions):
    out = list(samples)
    for p in positions:
        out[p] = float("nan")
    return out
