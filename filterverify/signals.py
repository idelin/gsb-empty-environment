"""Deterministic test-signal generation: impulse, sweep, multitone, probe."""
from __future__ import annotations

import math
import random


def impulse(n: int, amplitude: float = 1.0, index: int = 0) -> list[float]:
    x = [0.0] * n
    x[index] = amplitude
    return x


def log_chirp(n: int, fs: float, f0: float, f1: float) -> list[float]:
    """Logarithmic swept sine from f0 to f1 over n samples.

    Instantaneous frequency f(t) = f0 * (f1/f0)^(t/T), T = n/fs.
    """
    if not 0.0 < f0 < f1 <= fs / 2.0:
        raise ValueError("require 0 < f0 < f1 <= fs/2")
    T = n / fs
    r = f1 / f0
    lnr = math.log(r)
    return [
        math.sin(2.0 * math.pi * f0 * T / lnr * (math.exp(lnr * (k / fs) / T) - 1.0))
        for k in range(n)
    ]


def chirp_instantaneous_frequency(t: float, T: float, f0: float, f1: float) -> float:
    return f0 * (f1 / f0) ** (t / T)


def multitone(
    freqs: list[float], fs: float, n: int, seed: int = 0, amplitude: float = 1.0
) -> list[float]:
    """Sum of cosines with seeded random phases; total amplitude normalized."""
    for f in freqs:
        if not 0.0 < f < fs / 2.0:
            raise ValueError(f"tone {f} Hz outside (0, fs/2); would alias")
    rng = random.Random(seed)
    phases = [rng.uniform(0.0, 2.0 * math.pi) for _ in freqs]
    amp = amplitude / max(1, len(freqs))
    out = []
    for k in range(n):
        t = k / fs
        out.append(
            amp * sum(math.cos(2.0 * math.pi * f * t + p) for f, p in zip(freqs, phases))
        )
    return out


def stepped_sweep(
    freqs: list[float], fs: float, samples_per_step: int, amplitude: float = 1.0
) -> list[float]:
    """Stepped-sine sweep: each frequency held for samples_per_step samples.

    Phase is continuous within a step and restarts at each step; analysis
    fits each step after a settling margin, so no chirp-rate de-smearing is
    needed.  This is the 'sweep' test's signal.
    """
    out = []
    for f in freqs:
        if not 0.0 < f < fs / 2.0:
            raise ValueError(f"sweep step {f} Hz outside (0, fs/2)")
        for k in range(samples_per_step):
            out.append(amplitude * math.sin(2.0 * math.pi * f * k / fs))
    return out


def log_spaced(f0: float, f1: float, count: int) -> list[float]:
    if count < 2:
        return [f0]
    ratio = (f1 / f0) ** (1.0 / (count - 1))
    return [f0 * ratio**i for i in range(count)]


def probe_signal(n: int, fs: float, seed: int) -> list[float]:
    """Replayable counterexample probe: fixed tones + seeded noise.

    Fully determined by (n, fs, seed); stored in counterexample records so
    any failing run can be replayed exactly.
    """
    rng = random.Random(seed)
    freqs = (0.05 * fs, 0.13 * fs, 0.29 * fs)
    noise = [rng.uniform(-0.1, 0.1) for _ in range(n)]
    return [
        0.3 * sum(math.sin(2.0 * math.pi * f * k / fs) for f in freqs) + noise[k]
        for k in range(n)
    ]
