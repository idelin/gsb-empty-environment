"""Measurement primitives: tone projection, delay estimation, dB helpers."""
from __future__ import annotations

from math import cos, sin, pi, fsum, hypot, atan2, log10

from .dsp import cross_correlate


def to_db(x: float) -> float:
    return 20.0 * log10(max(x, 1e-300))


def from_db(db: float) -> float:
    return 10.0 ** (db / 20.0)


def quadrature_tone(y, freq: float, fs: float, start: int = 0, count: int | None = None):
    """Least-squares amplitude/phase of a tone of *known* frequency.

    Window-free: the segment is projected onto cos/sin at the exact
    frequency, so a unity-gain tone reads exactly its amplitude and no
    window-energy normalisation can fake attenuation.  At exactly fs/2
    the sine component vanishes for real signals; the amplitude is then
    recovered from the cosine projection alone and phase is None.
    Returns (amplitude, phase_radians_or_None).
    """
    n = len(y) - start if count is None else min(count, len(y) - start)
    if n <= 0:
        raise ValueError("empty analysis segment")
    if abs(freq - 0.5 * fs) <= 1e-9 * fs:
        c = fsum(y[start + i] * (1.0 if (start + i) % 2 == 0 else -1.0)
                 for i in range(n))
        return abs(c / n), None
    w = 2.0 * pi * freq / fs
    c = fsum(y[start + i] * cos(w * (start + i)) for i in range(n))
    s = fsum(y[start + i] * sin(w * (start + i)) for i in range(n))
    a = 2.0 * c / n
    b = 2.0 * s / n
    return hypot(a, b), atan2(-b, a)


def estimate_delay(x, y, max_abs_lag: int | None = None) -> float:
    """Delay d (samples, fractional) such that y[n] ~ x[n-d].

    Uses the cross-correlation peak with parabolic interpolation.
    """
    lags, vals = cross_correlate(y, x)
    best_i = -1
    best_v = -1.0
    for i, lag in enumerate(lags):
        if max_abs_lag is not None and abs(lag) > max_abs_lag:
            continue
        v = abs(vals[i])
        if v > best_v:
            best_v, best_i = v, i
    if best_i < 0:
        raise ValueError("no lag in range")
    frac = 0.0
    if 0 < best_i < len(vals) - 1:
        y0 = abs(vals[best_i - 1])
        y1 = abs(vals[best_i])
        y2 = abs(vals[best_i + 1])
        denom = y0 - 2.0 * y1 + y2
        if denom != 0.0:
            frac = 0.5 * (y0 - y2) / denom
    return lags[best_i] + frac
