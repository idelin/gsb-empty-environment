"""Local fixtures: deterministic sample arrays, rates, kernels, chunkings.

Everything is generated locally with fixed seeds; no audio device, no I/O.
"""

import math
import random

INPUT_RATE = 48000  # source sample rate provided by the fixture


def design_lowpass(interp, decim, half_width):
    """Windowed-sinc lowpass for interp/decim resampling.

    Returns ``2 * half_width * interp + 1`` taps sampled at ``interp``
    times the input rate, DC-normalised to ``interp``.
    """
    size = 2 * half_width * interp + 1
    center = (size - 1) / 2.0
    fc = 0.45 / max(interp, decim)  # cycles per kernel sample
    taps = []
    for i in range(size):
        x = i - center
        h = 2.0 * fc if x == 0 else math.sin(2.0 * math.pi * fc * x) / (math.pi * x)
        w = 0.54 + 0.46 * math.cos(math.pi * x / (center + 1.0))  # Hamming
        taps.append(h * w)
    norm = interp / sum(taps)
    return [t * norm for t in taps]


def tone(n, freq=440.0, rate=INPUT_RATE, amp=0.8, phase=0.0):
    return [amp * math.sin(2.0 * math.pi * freq * i / rate + phase)
            for i in range(n)]


def mixed_signal(n, seed=20260926, rate=INPUT_RATE):
    """Sum of sines plus deterministic noise: a non-trivial test signal."""
    rng = random.Random(seed)
    out = []
    for i in range(n):
        v = (0.50 * math.sin(2.0 * math.pi * 440.0 * i / rate)
             + 0.30 * math.sin(2.0 * math.pi * 1320.0 * i / rate + 0.7)
             + 0.15 * math.sin(2.0 * math.pi * 5000.0 * i / rate + 1.9))
        v += 0.02 * (rng.random() - 0.5)
        out.append(v)
    return out


def impulse(n, at=0, amp=1.0):
    out = [0.0] * n
    out[at] = amp
    return out


def chunk_split(total, seed, allow_empty=True):
    """Random chunk sizes covering `total`, incl. empty and 1-sample chunks."""
    rng = random.Random(seed)
    sizes = []
    remaining = total
    while remaining > 0:
        choices = [1, 2, rng.randint(3, 97)]
        if allow_empty:
            choices.append(0)
        k = min(rng.choice(choices), remaining)
        sizes.append(k)
        remaining -= k
    return sizes


def gap_positions(n, seed, count):
    """Deterministic, sorted, unique gap positions away from the edges."""
    rng = random.Random(seed)
    pos = sorted(rng.sample(range(8, n - 8), count))
    return pos


def with_gaps(samples, positions):
    """Copy of `samples` with gap positions replaced by None markers."""
    out = list(samples)
    for p in positions:
        out[p] = None
    return out
