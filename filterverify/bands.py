"""Explicit frequency-band specification with normalization conventions."""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class BandSpec:
    """Lowpass-style band plan in Hz.

    ``passband`` and ``stopband`` are (low, high) edges in Hz.  All edges
    must lie in ``[0, fs/2)``; a tone or band edge at or above Nyquist
    cannot be represented unambiguously by real samples and is rejected
    here rather than silently aliased.
    """

    sample_rate: float
    passband: tuple[float, float]
    stopband: tuple[float, float]

    def __post_init__(self) -> None:
        fs = self.sample_rate
        if fs <= 0:
            raise ValueError("sample_rate must be positive")
        nyquist = fs / 2.0
        for name, band in (("passband", self.passband), ("stopband", self.stopband)):
            lo, hi = band
            if not (0.0 <= lo < hi):
                raise ValueError(f"{name} edges must satisfy 0 <= lo < hi, got {band}")
            if hi >= nyquist:
                raise ValueError(
                    f"{name} edge {hi} Hz >= Nyquist {nyquist} Hz "
                    f"(normalized {hi / fs:.4f} >= 0.5); band edges above "
                    "Nyquist alias and cannot be verified"
                )
        if self.passband[1] >= self.stopband[0]:
            raise ValueError("passband and stopband must not overlap")

    @property
    def nyquist(self) -> float:
        return self.sample_rate / 2.0

    def normalized(self, freq_hz: float) -> float:
        """Normalized frequency convention: f / fs, Nyquist = 0.5."""
        return freq_hz / self.sample_rate
