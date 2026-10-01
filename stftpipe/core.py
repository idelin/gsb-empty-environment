"""STFT core: frame scheduling, per-frame analysis/synthesis, constants.

Declarations:
- Algorithmic latency: one window length (size samples). A frame's covered
  interval can only be emitted once the whole window has arrived.
- Numerical tolerance: NUMERICAL_TOLERANCE = 1e-9 (pure-python float64).
"""
from __future__ import annotations

from .fft import fft, ifft
from .windows import get_window

NUMERICAL_TOLERANCE = 1e-9
WEIGHT_EPS = 1e-12


def build_starts(size, hops, n_samples):
    """Frame start positions from a (cycled) hop sequence covering
    [0, n_samples).

    Frames are also prepended at negative starts (hop sequence cycled
    backwards) so the first samples receive full window coverage instead
    of a degenerate zero-weight edge.
    """
    if n_samples <= 0:
        return []
    starts = [0]
    i = 0
    while True:
        nxt = starts[-1] + hops[i % len(hops)]
        if nxt >= n_samples:
            break
        starts.append(nxt)
        i += 1
    j = 1
    while starts[0] - hops[(-j) % len(hops)] > -size:
        starts.insert(0, starts[0] - hops[(-j) % len(hops)])
        j += 1
    return starts


class STFTKernel:
    def __init__(self, size, window="hann"):
        if size <= 0:
            raise ValueError("window size must be positive")
        self.size = int(size)
        self.window_name = window
        self.window = get_window(window, self.size)

    @property
    def latency_samples(self):
        return self.size

    def analyze_frame(self, samples, start):
        size = self.size
        win = self.window
        n = len(samples)
        seg = [
            (samples[start + i] if 0 <= start + i < n else 0.0) * win[i]
            for i in range(size)
        ]
        return fft(seg)

    def synthesize_frame(self, spectrum):
        return [z.real for z in ifft(spectrum)]
