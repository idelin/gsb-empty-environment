"""Local synthetic fixtures: sample arrays and sample rate only.

No audio devices are used anywhere in this project.
"""

from __future__ import annotations

import random

FS = 100.0  # Hz, declared sample rate for every fixture

WINDOW_LEN = 128
HOP = 64


def white_series(n: int, seed: int) -> list[float]:
    rng = random.Random(seed)
    series = [rng.gauss(0.0, 1.0) for _ in range(n)]
    # Light two-pass moving average makes linear interpolation of fractional
    # delays well conditioned (band-limited-ish) without any third-party code.
    width = 3
    smoothed = list(series)

    def forward(data):
        out = [0.0] * n
        for i in range(n):
            acc = 0.0
            count = 0
            for j in range(max(0, i - width + 1), i + 1):
                acc += data[j]
                count += 1
            out[i] = acc / count
        return out

    # Forward-backward (zero-phase) smoothing: the cascade has zero group
    # delay, so fractional-delay synthesis is not biased by the filter.
    for _ in range(3):
        fwd = forward(smoothed)
        rev = forward(fwd[::-1])
        smoothed = rev[::-1]
    return smoothed


def _sample_fractional(x: list[float], index: float) -> float:
    """Linear-interpolation sample of x at (possibly fractional) index."""
    if index <= 0:
        return x[0]
    if index >= len(x) - 1:
        return x[-1]
    lo = int(index)
    frac = index - lo
    return x[lo] * (1.0 - frac) + x[lo + 1] * frac


def drifting_pair(
    n: int = 900,
    d0: float = 3.5,
    slope: float = 0.2,
    seed: int = 42,
    missing_fraction: float = 0.06,
    polarity_b: int = 1,
    noise_sigma: float = 0.01,
) -> tuple[list[float], list[float], list[bool], list[bool]]:
    """Synthesize b[n] ~= polarity * a[n - d(window_center)].

    Delay grows linearly with sample index at ``slope`` samples per window
    (the estimator's velocity grid is samples/window with hop HOP samples).
    """
    rng = random.Random(seed)
    a = white_series(n + 32, seed)
    b = [0.0] * n
    delay_at = [0.0] * n
    slope_per_sample = slope / HOP
    for i in range(n):
        d_i = d0 + slope_per_sample * i
        delay_at[i] = d_i
        value = _sample_fractional(a, i - d_i)
        b[i] = polarity_b * value + rng.gauss(0.0, noise_sigma)
    a = a[:n]

    mask_a = [True] * n
    mask_b = [True] * n
    rng2 = random.Random(seed + 1000)
    for i in range(n):
        if rng2.random() < missing_fraction:
            mask_a[i] = False
        if rng2.random() < missing_fraction:
            mask_b[i] = False
    return a, b, mask_a, mask_b, delay_at


def periodic_pair(n: int = WINDOW_LEN, period: int = 4, lag: int = 0,
                  polarity_b: int = 1) -> tuple[list[float], list[float]]:
    """Impulse train with ``period``; produces multiple equal NCC peaks."""
    a = [1.0 if i % period == 0 else 0.0 for i in range(n)]
    if lag >= 0:
        b = [0.0] * lag + [polarity_b * v for v in a[: n - lag]]
    else:
        b = [polarity_b * v for v in a[-lag:]] + [0.0] * (-lag)
    return a, b


def unique_delay_pair(
    n: int = WINDOW_LEN,
    lag: int = 4,
    seed: int = 7,
    polarity_b: int = 1,
    noise_sigma: float = 0.02,
) -> tuple[list[float], list[float]]:
    """A short white-noise window with one strong NCC peak at ``lag``."""
    a = white_series(n + abs(lag) + 2, seed)
    rng = random.Random(seed + 500)
    b = [0.0] * n
    for i in range(n):
        src = i - lag
        if 0 <= src < len(a):
            b[i] = polarity_b * a[src] + rng.gauss(0.0, noise_sigma)
    return a[:n], b
