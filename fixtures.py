"""Local deterministic fixtures: sample arrays, kernels and sample rates.

No audio devices are involved; everything is generated from fixed seeds so
tests and the demo are exactly replayable.
"""

from __future__ import annotations

import math
import random

# Sample rates used across the suite (Hz).
FS_VOICE = 16000
FS_AUDIO = 44100
FS_HIRES = 48000


def lowpass_kernel(num_taps, up, down, seed=1234):
    """Windowed-sinc lowpass for the polyphase resampler (gain ~= ``up``).

    Cutoff is 0.8 * min(input, output) Nyquist in the upsampled domain.
    A deterministic seed-based dither keeps the kernel generic (not exactly
    symmetric) so tests do not accidentally rely on symmetry.
    """
    rng = random.Random(seed)
    f0 = 0.5 * 0.8 / max(up, down)  # cycles per upsampled sample
    mid = (num_taps - 1) / 2.0
    h = []
    for n in range(num_taps):
        x = n - mid
        sinc = 2.0 * f0 if x == 0 else math.sin(2.0 * math.pi * f0 * x) / (math.pi * x)
        win = 0.54 - 0.46 * math.cos(2.0 * math.pi * n / (num_taps - 1))
        h.append(sinc * win * up + 1e-4 * (rng.random() - 0.5))
    return h


def tone_samples(n, fs, freqs, seed=7, noise=0.01):
    """Sum-of-sines test signal with a little deterministic noise."""
    rng = random.Random(seed)
    out = []
    for i in range(n):
        v = sum(math.sin(2.0 * math.pi * f * i / fs) for f in freqs)
        out.append(v + noise * (2.0 * rng.random() - 1.0))
    return out


def impulse(n):
    return [1.0] + [0.0] * (n - 1)


def random_chunk_sizes(total, seed, choices=(0, 0, 1, 1, 2, 3, 5, 8, 17, 64)):
    """Split ``total`` into random chunk sizes including empty/short blocks."""
    rng = random.Random(seed)
    sizes = []
    remaining = total
    while remaining > 0:
        s = rng.choice(choices)
        if s == 0:
            sizes.append(0)
            continue
        s = min(s, remaining)
        sizes.append(s)
        remaining -= s
    sizes.append(0)  # trailing empty block
    return sizes
