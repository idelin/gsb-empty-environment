"""Window functions. Periodic variants are the default (overlap-add use)."""
from __future__ import annotations

import math


def hann_periodic(n):
    return [0.5 - 0.5 * math.cos(2.0 * math.pi * k / n) for k in range(n)]


def hann_symmetric(n):
    if n == 1:
        return [1.0]
    return [0.5 - 0.5 * math.cos(2.0 * math.pi * k / (n - 1)) for k in range(n)]


def hamming_periodic(n):
    return [0.54 - 0.46 * math.cos(2.0 * math.pi * k / n) for k in range(n)]


def rect(n):
    return [1.0] * n


_WINDOWS = {
    "hann": hann_periodic,
    "hann_sym": hann_symmetric,
    "hamming": hamming_periodic,
    "rect": rect,
}


def get_window(name, n):
    try:
        fn = _WINDOWS[name]
    except KeyError:
        raise ValueError(f"unknown window: {name!r}, choices: {sorted(_WINDOWS)}") from None
    return fn(n)
