"""One-sided power spectrum via a direct DFT (standard library only).

Deliberately a naive O(N^2) transform: fixture grids are small and the
direct form is easy to audit against hand computation.
"""

import cmath
import math


def power_spectrum(samples):
    """One-sided power spectrum of a real sample sequence.

    Returns ``n // 2 + 1`` bins.  Bin power is ``(|X[k]| / N)**2`` with
    the one-sided doubling applied to all bins except DC and (for even
    N) Nyquist, so a pure sine of amplitude A at a bin-centre frequency
    yields bin power ``A**2 / 2``.
    """
    n = len(samples)
    if n == 0:
        raise ValueError("samples must be non-empty")
    half = n // 2 + 1
    spectrum = []
    for k in range(half):
        acc = 0j
        for t, x in enumerate(samples):
            acc += x * cmath.exp(-2j * math.pi * k * t / n)
        power = (abs(acc) / n) ** 2
        if 0 < k and (n % 2 == 1 or k < n // 2):
            power *= 2.0
        spectrum.append(power)
    return spectrum
