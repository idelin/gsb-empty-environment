"""Deterministic local sample fixtures.  No audio device is ever touched;
all sample arrays are generated in memory.  Standard library only."""
from __future__ import annotations

import math
import random

SAMPLE_RATE = 48000.0


def silence(n: int) -> list:
    return [0.0] * n


def sine_burst(freq=440.0, amp=0.5, n=4800, fs=SAMPLE_RATE) -> list:
    return [amp * math.sin(2.0 * math.pi * freq * i / fs) for i in range(n)]


def step_signal(amps=(0.1, 0.8, 0.02, 0.6), seg_len=1200) -> list:
    out = []
    for a in amps:
        out.extend([a] * seg_len)
    return out


def noise(n=4096, amp=0.3, seed=1234) -> list:
    rng = random.Random(seed)
    return [amp * (2.0 * rng.random() - 1.0) for _ in range(n)]


def impulse_train(n=4096, period=512, amp=0.9) -> list:
    return [amp if i % period == 0 else 0.0 for i in range(n)]


def multichannel_demo(fs=SAMPLE_RATE):
    """Two-channel fixture: loud sine on ch0, quiet stepped signal on ch1,
    including a silent stretch to exercise the dB floor."""
    n = 4800
    ch0 = sine_burst(freq=220.0, amp=0.9, n=n, fs=fs)
    ch1 = step_signal(amps=(0.05, 0.7, 0.0, 0.4), seg_len=n // 4)
    return [ch0, ch1]
