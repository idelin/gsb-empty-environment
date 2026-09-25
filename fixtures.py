"""Deterministic local fixtures: sample arrays + sample rate, no audio devices.

Rate is 8000 Hz and the default detection window is 80 samples (10 ms), so
all scenario positions below are exact multiples of the window.
"""

import math
import random

from silseg import Track

RATE = 8000
WINDOW = 80
LOW = 0.05
HIGH = 0.15

# Default parameter set shared by demo and tests.
PARAMS = dict(
    window=WINDOW,
    low_threshold=LOW,
    high_threshold=HIGH,
    strategy="any",
    min_silence=160,
    min_sound=0,
    pre_pad=80,
    post_pad=80,
)


def tone(freq, n, rate=RATE, amp=0.5):
    """Sine burst; RMS ~0.354, clearly above HIGH."""
    return [amp * math.sin(2.0 * math.pi * freq * i / rate) for i in range(n)]


def silence(n):
    """True silence: real 0.0 samples (distinct from a gap = no data)."""
    return [0.0] * n


def noise(n, amp=0.4, seed=1234):
    """Deterministic noise burst; RMS ~0.23, clearly above HIGH."""
    rng = random.Random(seed)
    return [rng.uniform(-amp, amp) for _ in range(n)]


def two_burst_samples():
    """5600 samples: tone at [800,1600) and [4000,4800), silence elsewhere."""
    return silence(800) + tone(440, 800) + silence(2400) + tone(550, 800) + silence(800)


def fixture_two_bursts():
    t = Track("main", RATE)
    t.add_samples(0, two_burst_samples())
    return [t]


def short_noise_samples():
    """Like two-burst, plus a 1-window noise blip at [2640,2720) (window 33)."""
    return (
        silence(800) + tone(440, 800) + silence(1040) + noise(80)
        + silence(1280) + tone(550, 800) + silence(800)
    )


def fixture_short_noise():
    t = Track("main", RATE)
    t.add_samples(0, short_noise_samples())
    return [t]


def padding_bridge_samples():
    """Two 400-sample bursts 400 samples apart: [800,1200) and [1600,2000)."""
    return silence(800) + tone(440, 400) + silence(400) + tone(550, 400) + silence(800)


def fixture_padding_bridge():
    t = Track("main", RATE)
    t.add_samples(0, padding_bridge_samples())
    return [t]


def fixture_gap_bridge():
    """Track with a real gap (no data) at [1600,4000) between two bursts."""
    t = Track("main", RATE)
    t.add_samples(0, silence(800) + tone(440, 800))
    t.add_samples(4000, tone(550, 800) + silence(800))
    return [t]


def fixture_multitrack():
    """Two full-length tracks whose loud regions never overlap."""
    a = Track("a", RATE)
    a.add_samples(0, silence(800) + tone(440, 800) + silence(3200))
    b = Track("b", RATE)
    b.add_samples(0, silence(2400) + tone(330, 800) + silence(1600))
    return [a, b]


def fixture_gap_vs_silence():
    """One track: burst, then a gap (no data), then true silence (zeros)."""
    t = Track("g", RATE)
    t.add_samples(0, silence(800) + tone(440, 800))
    t.add_samples(3200, silence(1600))
    return [t]
