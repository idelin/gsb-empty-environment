"""Filter specification: explicit bands, declared latency and tolerances.

Conventions
-----------
* Frequencies are in Hz, times in samples, amplitudes are linear peak.
* dB figures are 20*log10 of linear amplitude ratios.
* passband: |H| must stay within +/- passband_ripple_db of unity.
* stopband: |H| must be at least stopband_atten_db below unity.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class FilterSpec:
    sample_rate: float
    passband: tuple          # (f_lo, f_hi) Hz, inclusive
    stopband: tuple          # (f_lo, f_hi) Hz, inclusive
    passband_ripple_db: float = 0.5
    stopband_atten_db: float = 40.0
    max_latency_samples: float = 10000.0
    latency_tolerance_samples: float = 1.0
    chunk_tolerance: float = 1e-9      # max |sample| mismatch between chunkings
    shift_tolerance: float = 1e-9      # max |sample| mismatch under time shift
    nyquist_guard_hz: float = 100.0    # test tones stay this far below fs/2

    def validate(self) -> None:
        if self.sample_rate <= 0:
            raise ValueError("sample_rate must be positive")
        p_lo, p_hi = self.passband
        s_lo, s_hi = self.stopband
        if not 0.0 <= p_lo < p_hi:
            raise ValueError("bad passband")
        if not s_lo < s_hi <= self.sample_rate / 2.0:
            raise ValueError("bad stopband")
        if p_hi >= s_lo:
            raise ValueError("passband and stopband must not overlap")
        if self.passband_ripple_db <= 0 or self.stopband_atten_db <= 0:
            raise ValueError("dB limits must be positive")
