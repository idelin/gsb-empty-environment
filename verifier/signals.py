"""Deterministic test-signal generators: impulse, sweep, multitone, noise.

All generators are pure functions of their arguments (plus an explicit
seed where randomness is needed), so every stimulus is replayable.
"""
from __future__ import annotations

import math
import random
from math import cos, sin, pi, log, exp, fsum


def impulse(n: int, amplitude: float = 1.0):
    if n < 1:
        raise ValueError("n must be >= 1")
    return [float(amplitude)] + [0.0] * (n - 1)


def tone(freq: float, amplitude: float, n: int, fs: float, phase: float = 0.0):
    w = 2.0 * pi * freq / fs
    return [amplitude * cos(w * i + phase) for i in range(n)]


def log_chirp(n: int, fs: float, f0: float, f1: float,
              amplitude: float = 0.8, fade: int = 128):
    """Logarithmic swept sine from f0 to f1 with raised-cosine end fades."""
    if not 0.0 < f0 < f1 <= fs / 2.0:
        raise ValueError("need 0 < f0 < f1 <= fs/2")
    t_total = (n - 1) / fs
    k = log(f1 / f0)
    out = []
    for i in range(n):
        t = i / fs
        phase = 2.0 * pi * f0 * t_total / k * (exp(k * t / t_total) - 1.0)
        out.append(amplitude * sin(phase))
    f = max(0, min(fade, n // 2))
    for i in range(f):
        g = 0.5 - 0.5 * cos(pi * i / f)
        out[i] *= g
        out[n - 1 - i] *= g
    return out


def snap_to_bin(freq: float, n: int, fs: float) -> float:
    """Snap a frequency to the nearest DFT bin of an n-point analysis."""
    return round(freq * n / fs) * fs / n


def bin_grid(f_lo: float, f_hi: float, n: int, fs: float):
    """All DFT bin-centre frequencies of an n-point analysis in [f_lo, f_hi]."""
    k_lo = max(1, math.ceil(f_lo * n / fs - 1e-9))
    k_hi = min(n // 2, math.floor(f_hi * n / fs + 1e-9))
    return [k * fs / n for k in range(k_lo, k_hi + 1)]


def multitone(freqs, amplitude: float, n: int, fs: float, seed: int):
    """Sum of cosines with deterministic random phases.

    Frequencies should be bin-aligned to the analysis segment so the
    tones are mutually orthogonal under quadrature projection.
    """
    freqs = list(freqs)
    rng = random.Random(seed)
    phases = [rng.uniform(0.0, 2.0 * pi) for _ in freqs]
    per = amplitude / max(1, len(freqs))
    ws = [2.0 * pi * f / fs for f in freqs]
    return [fsum(per * cos(w * i + p) for w, p in zip(ws, phases)) for i in range(n)]


def uniform_noise(n: int, seed: int, low: float = -0.5, high: float = 0.5):
    rng = random.Random(seed)
    return [rng.uniform(low, high) for _ in range(n)]
