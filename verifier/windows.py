"""Window functions and correctly normalised spectra.

Conventions
-----------
* Amplitudes are linear peak amplitudes of real sinusoids.
* A windowed spectrum is normalised by the window *coherent gain*
  (sum(w)/N), never by the window energy (sum(w^2) or RMS).  Using the
  energy here would misread a unity-gain tone as an attenuated one
  (a Hann window would fake ~4.2 dB of loss).
"""
from __future__ import annotations

from math import cos, pi, fsum

from .dsp import fft, next_pow2


def hann(n: int):
    if n <= 1:
        return [1.0] * max(n, 0)
    return [0.5 - 0.5 * cos(2.0 * pi * i / (n - 1)) for i in range(n)]


def blackman(n: int):
    if n <= 1:
        return [1.0] * max(n, 0)
    return [
        0.42 - 0.5 * cos(2.0 * pi * i / (n - 1)) + 0.08 * cos(4.0 * pi * i / (n - 1))
        for i in range(n)
    ]


def coherent_gain(w) -> float:
    if not w:
        raise ValueError("empty window")
    return fsum(w) / len(w)


def amplitude_spectrum(x, fs: float, window=None):
    """One-sided amplitude spectrum normalised by coherent gain.

    A real tone A*cos(2*pi*f*t + phi) sitting on a bin centre reads as
    amplitude A.  DC and the Nyquist bin are not doubled (they have no
    negative-frequency image).
    """
    n = len(x)
    if n == 0:
        raise ValueError("empty signal")
    w = list(window) if window is not None else [1.0] * n
    if len(w) != n:
        raise ValueError("window length mismatch")
    cg = fsum(w)
    if cg == 0.0:
        raise ValueError("degenerate window")
    xw = [a * b for a, b in zip(x, w)]
    m = next_pow2(n)
    spec = fft(xw + [0.0] * (m - n))
    freqs = [k * fs / m for k in range(m // 2 + 1)]
    amps = [0.0] * (m // 2 + 1)
    amps[0] = abs(spec[0]) / cg
    for k in range(1, m // 2):
        amps[k] = 2.0 * abs(spec[k]) / cg
    amps[m // 2] = abs(spec[m // 2]) / cg
    return freqs, amps
