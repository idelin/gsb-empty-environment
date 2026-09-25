"""Reference streaming FIR filter and windowed-sinc design."""
from __future__ import annotations

from math import sin, pi, fsum

from .windows import blackman, hann


def _sinc(x: float) -> float:
    return 1.0 if x == 0.0 else sin(pi * x) / (pi * x)


def design_lowpass(n_taps: int, cutoff_hz: float, fs: float, window: str = "blackman"):
    """Windowed-sinc lowpass, DC gain normalised to exactly 1."""
    if n_taps < 3:
        raise ValueError("need at least 3 taps")
    windows = {
        "blackman": blackman(n_taps),
        "hann": hann(n_taps),
        "rectangular": [1.0] * n_taps,
    }
    if window not in windows:
        raise ValueError(f"unknown window {window!r}")
    w = windows[window]
    fc = cutoff_hz / fs
    mid = (n_taps - 1) / 2.0
    h = [2.0 * fc * _sinc(2.0 * fc * (n - mid)) * w[n] for n in range(n_taps)]
    g = fsum(h)
    return [v / g for v in h]


class StreamingFIR:
    """Synchronous streaming FIR (direct form, full history kept).

    process() returns exactly len(chunk) samples; finalize() flushes the
    remaining len(h)-1 tail samples of the full convolution.  Declared
    latency defaults to the linear-phase group delay (len(h)-1)/2.
    """

    def __init__(self, h, latency_samples: float | None = None):
        if not h:
            raise ValueError("empty impulse response")
        self.h = [float(v) for v in h]
        if latency_samples is None:
            self.latency_samples = (len(h) - 1) / 2.0
        else:
            self.latency_samples = float(latency_samples)
        self.reset()

    def reset(self):
        self._hist = []
        self._finalized = False

    def process(self, chunk):
        if self._finalized:
            raise RuntimeError("process() called after finalize()")
        chunk = [float(v) for v in chunk]
        base = len(self._hist)
        self._hist.extend(chunk)
        h = self.h
        m_taps = len(h)
        hist = self._hist
        out = []
        for i in range(len(chunk)):
            n = base + i
            acc = 0.0
            for m in range(min(n + 1, m_taps)):
                acc += h[m] * hist[n - m]
            out.append(acc)
        return out

    def finalize(self):
        self._finalized = True
        h = self.h
        m_taps = len(h)
        hist = self._hist
        n_total = len(hist)
        out = []
        for k in range(m_taps - 1):
            n = n_total + k
            acc = 0.0
            for m in range(k + 1, m_taps):
                j = n - m
                if 0 <= j < n_total:
                    acc += h[m] * hist[j]
            out.append(acc)
        return out
