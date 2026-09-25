"""Conservative coefficient-based bounds between frequency grid points.

For an FIR with taps h[0..M],
    |dH/df| <= L,  L = (2*pi/fs) * sum_n n*|h[n]|,
so on a grid interval of width d the true |H| stays within
    [ min(endpoints) - L*d/2,  max(endpoints) + L*d/2 ].
This certifies (or refutes) a band specification *between* measured or
evaluated grid points - something a finite sweep alone can never do.
"""
from __future__ import annotations

import cmath
import math
from dataclasses import dataclass
from math import fsum

from .metrics import from_db


def fir_response(h, freq: float, fs: float) -> complex:
    z = cmath.exp(-2j * math.pi * freq / fs)
    acc = 0j
    p = 1.0 + 0.0j
    for hn in h:
        acc += hn * p
        p *= z
    return acc


def lipschitz_constant(h, fs: float) -> float:
    """Per-Hz bound on |dH/df| for the given taps."""
    return 2.0 * math.pi / fs * fsum(n * abs(hn) for n, hn in enumerate(h))


@dataclass
class BandCertificate:
    band: tuple
    status: str            # "certified" | "violated" | "undetermined"
    grid_points: int
    interval_hz: float
    lipschitz: float
    worst_upper_bound: float
    worst_lower_bound: float
    lower_limit: float
    upper_limit: float
    detail: str = ""


def certify_band(h, fs: float, f_lo: float, f_hi: float,
                 lower: float, upper: float, max_points: int = 200001) -> BandCertificate:
    """Adaptively refine a uniform grid until the Lipschitz bound either
    certifies lower <= |H| <= upper everywhere, a grid point violates it,
    or the point budget is exhausted (undetermined)."""
    L = lipschitz_constant(h, fs)
    n = 65
    while True:
        step = (f_hi - f_lo) / (n - 1)
        vals = [abs(fir_response(h, f_lo + i * step, fs)) for i in range(n)]
        for v in vals:
            if v < lower or v > upper:
                return BandCertificate(
                    (f_lo, f_hi), "violated", n, step, L,
                    max(vals), min(vals), lower, upper,
                    "grid point outside limits")
        half = L * step / 2.0
        worst_u = max(max(vals[i], vals[i + 1]) + half for i in range(n - 1))
        worst_l = min(max(0.0, min(vals[i], vals[i + 1]) - half) for i in range(n - 1))
        if worst_u <= upper and worst_l >= lower:
            return BandCertificate(
                (f_lo, f_hi), "certified", n, step, L,
                worst_u, worst_l, lower, upper,
                "Lipschitz bound covers every inter-grid interval")
        if 2 * n - 1 > max_points:
            return BandCertificate(
                (f_lo, f_hi), "undetermined", n, step, L,
                worst_u, worst_l, lower, upper,
                "bound straddles limits; refine grid (raise max_points)")
        n = 2 * n - 1


def certify_fir(h, spec) -> dict:
    """Certify passband ripple and stopband attenuation from coefficients."""
    fs = spec.sample_rate
    r = spec.passband_ripple_db
    a = spec.stopband_atten_db
    pb = certify_band(h, fs, spec.passband[0], spec.passband[1],
                      from_db(-r), from_db(r))
    sb = certify_band(h, fs, spec.stopband[0],
                      min(spec.stopband[1], fs / 2.0), 0.0, from_db(-a))
    return {"passband": pb, "stopband": sb}
