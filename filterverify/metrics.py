"""Measurements: tone fits, window-normalized spectra, lag, band metrics."""
from __future__ import annotations

import math


class IllConditionedError(ValueError):
    """Raised when a tone fit degenerates (e.g. f exactly at Nyquist)."""


def tone_fit(
    x: list[float], freq: float, fs: float, start: int = 0
) -> tuple[float, float, float]:
    """Least-squares fit of a*cos(w*(k+start)) + b*sin(w*(k+start)).

    Returns (amplitude, phase, residual_rms) with phase = atan2(b, a).
    ``start`` is the absolute sample index of x[0], so phases of segments
    taken at different positions remain comparable on one time axis.

    Near fs/2 the sin basis degenerates (sin(pi*k) == 0 at exactly fs/2);
    the Gram determinant is checked and IllConditionedError raised instead
    of returning a meaningless amplitude.
    """
    if not 0.0 < freq < fs / 2.0:
        raise ValueError(f"tone fit requires 0 < f < fs/2, got {freq} (fs={fs})")
    n = len(x)
    if n < 4:
        raise ValueError("tone fit needs at least 4 samples")
    w = 2.0 * math.pi * freq / fs
    cc = cs = ss = xc = xs = 0.0
    for k, v in enumerate(x):
        c = math.cos(w * (k + start))
        s = math.sin(w * (k + start))
        cc += c * c
        ss += s * s
        cs += c * s
        xc += v * c
        xs += v * s
    det = cc * ss - cs * cs
    # For a well-posed fit det ~ (n/2)^2; require a sane fraction of that.
    if det < 1e-9 * (n / 2.0) ** 2:
        raise IllConditionedError(
            f"tone fit ill-conditioned at {freq} Hz (fs={fs}, n={n}, "
            f"det={det:.3e}); sin/cos basis near-degenerate (f too close "
            "to 0 or fs/2 for this many samples)"
        )
    a = (xc * ss - xs * cs) / det
    b = (xs * cc - xc * cs) / det
    amp = math.hypot(a, b)
    phase = math.atan2(b, a)
    res = 0.0
    for k, v in enumerate(x):
        d = v - a * math.cos(w * (k + start)) - b * math.sin(w * (k + start))
        res += d * d
    return amp, phase, math.sqrt(res / n)


def hann(n: int) -> list[float]:
    if n < 2:
        return [1.0] * n
    return [0.5 - 0.5 * math.cos(2.0 * math.pi * k / (n - 1)) for k in range(n)]


def windowed_tone_amplitude(
    x: list[float], freq: float, fs: float, window: list[float] | None = None
) -> float:
    """Tone amplitude via windowed DFT with coherent-gain normalization.

    amplitude = 2 * |sum(x[n] w[n] e^{-j w n})| / sum(w[n]).

    Dividing by N (or by sum(w^2), the energy/ENBW normalization) instead
    of the coherent gain sum(w) mis-reports a unity-gain tone as
    attenuated (factor 0.5 for Hann).  Power/ENBW normalization is for
    noise densities, not for coherent tone amplitude.
    """
    if window is None:
        window = hann(len(x))
    coherent_gain = sum(window)
    if coherent_gain <= 0.0:
        raise ValueError("window has non-positive coherent gain")
    w = 2.0 * math.pi * freq / fs
    re = im = 0.0
    for k, v in enumerate(x):
        e = v * window[k]
        re += e * math.cos(w * k)
        im -= e * math.sin(w * k)
    return 2.0 * math.hypot(re, im) / coherent_gain


def estimate_lag(x: list[float], y: list[float], max_lag: int) -> tuple[int, float]:
    """Lag of y behind x maximizing cross-correlation, over [0, max_lag]."""
    best_lag, best_val = 0, float("-inf")
    n = min(len(x), len(y))
    for lag in range(max_lag + 1):
        acc = 0.0
        for k in range(n - lag):
            acc += x[k] * y[k + lag]
        if acc > best_val:
            best_val, best_lag = acc, lag
    return best_lag, best_val


def max_abs_diff(a: list[float], b: list[float]) -> tuple[float, int]:
    """(max |a-b|, index) over the common prefix."""
    worst, worst_i = 0.0, -1
    for i in range(min(len(a), len(b))):
        d = abs(a[i] - b[i])
        if d > worst:
            worst, worst_i = d, i
    return worst, worst_i


def wrap_phase(phi: float) -> float:
    return (phi + math.pi) % (2.0 * math.pi) - math.pi


def to_db(ratio: float) -> float:
    return 20.0 * math.log10(max(ratio, 1e-300))
