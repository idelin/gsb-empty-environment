"""filterverify: black-box verification of streamable audio filter implementations.

Conventions
-----------
- Frequencies are given in Hz together with the sample rate ``fs`` (Hz).
  Normalized frequency is ``f / fs``; the Nyquist limit is ``0.5 * fs``
  (normalized 0.5).  All band edges must satisfy ``0 <= f < fs / 2``.
- Windowed spectral estimates use *coherent gain* normalization:
  a tone of amplitude A measured through window ``w`` yields
  ``A = 2 * |sum(x[n] * w[n] * exp(-2j*pi*f*n/fs))| / sum(w[n])``.
  Dividing by ``N`` instead of ``sum(w[n])`` under-reports unity gain by the
  window's coherent gain (0.5 for Hann) and is a bug, not attenuation.
- Least-squares tone fits model ``x[n] = a*cos(w*n) + b*sin(w*n)`` with
  ``w = 2*pi*f/fs``; amplitude ``hypot(a, b)``, phase ``atan2(b, a)``.
  The fit degenerates as ``f -> fs/2`` (the sin basis vanishes); the
  condition of the 2x2 Gram matrix is checked and reported instead of
  silently returning garbage.
- Implementations under test declare ``latency_samples`` (group delay).
  The verifier aligns output by the declared latency and reports the
  *measured minus declared* offset separately.
"""
from .bands import BandSpec
from .verifier import Tolerances, VerifierConfig, verify
from .report import MeasurementRecord, VerificationReport

__all__ = [
    "BandSpec",
    "Tolerances",
    "VerifierConfig",
    "verify",
    "MeasurementRecord",
    "VerificationReport",
]
