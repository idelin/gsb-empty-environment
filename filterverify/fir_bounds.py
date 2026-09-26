"""Conservative full-band error bounds for FIR filters from their coefficients.

For H(f) = sum_n h[n] e^{-j 2 pi f n / fs} the linear-phase term dominates
the raw derivative, so we demodulate: with tau = (M-1)/2,

    G(f) = H(f) e^{j 2 pi f tau / fs},   |G| = |H|,
    G'(f) = sum_n h[n] * j 2 pi (tau-n)/fs * e^{j 2 pi f (tau-n)/fs}.

G' is evaluated *exactly* on the grid, and between grid points

    |G'(f)| <= max_k |G'(f_k)| + D2 * df/2,
    D2 = (2 pi / fs)^2 * sum_n (n - tau)^2 * |h[n]|   (bound on |G''|),

so |H(f)| is bracketed on the whole interval by

    upper:  max over intervals of (|H_k| + |H_{k+1}|)/2 + E*df/2
    lower:  max(0, min_k |H(f_k)| - E*df/2),   E = max_k |G'_k| + D2*df/2.

A certificate over the *entire* band therefore follows from finitely many
evaluations plus the derivative bound -- unlike a bare sweep, which says
nothing between its measurement points.
"""
from __future__ import annotations

import cmath
import math
from dataclasses import dataclass


def fir_response(coeffs: list[float], freq: float, fs: float) -> complex:
    w = 2.0 * math.pi * freq / fs
    return sum(h * cmath.exp(-1j * w * n) for n, h in enumerate(coeffs))


def derivative_bound(coeffs: list[float], fs: float) -> float:
    """Crude upper bound on |G'| in 1/Hz for the demodulated response."""
    m = len(coeffs) - 1
    tau = m / 2.0
    return 2.0 * math.pi / fs * sum(abs(n - tau) * abs(h) for n, h in enumerate(coeffs))


def _demodulated_derivative(coeffs: list[float], freq: float, fs: float) -> float:
    """Exact |G'(freq)| for G(f) = H(f) e^{j 2 pi f tau / fs}."""
    m = len(coeffs) - 1
    tau = m / 2.0
    total = 0j
    for n, h in enumerate(coeffs):
        total += h * (1j * 2.0 * math.pi * (tau - n) / fs) * cmath.exp(
            1j * 2.0 * math.pi * freq * (tau - n) / fs
        )
    return abs(total)


def _second_derivative_bound(coeffs: list[float], fs: float) -> float:
    """Upper bound on |G''| in 1/Hz^2 (demodulated, centered at tau)."""
    m = len(coeffs) - 1
    tau = m / 2.0
    return (2.0 * math.pi / fs) ** 2 * sum(
        (n - tau) ** 2 * abs(h) for n, h in enumerate(coeffs)
    )


@dataclass
class BandBounds:
    f_lo: float
    f_hi: float
    grid_points: int
    grid_spacing_hz: float
    max_magnitude_grid: float
    min_magnitude_grid: float
    between_grid_margin: float  # E * df / 2, reported separately as resolution
    upper_bound: float  # conservative max |H| over the whole interval
    lower_bound: float  # conservative min |H| over the whole interval (>= 0)


def fir_band_bounds(
    coeffs: list[float], fs: float, f_lo: float, f_hi: float, grid_points: int = 257
) -> BandBounds:
    if grid_points < 2:
        raise ValueError("need at least 2 grid points")
    df = (f_hi - f_lo) / (grid_points - 1)
    grid = [f_lo + i * df for i in range(grid_points)]
    mags = [abs(fir_response(coeffs, f, fs)) for f in grid]
    max_deriv = max(_demodulated_derivative(coeffs, f, fs) for f in grid)
    slope_bound = max_deriv + _second_derivative_bound(coeffs, fs) * df / 2.0
    margin = slope_bound * df / 2.0
    # per-interval tight envelope: max of min(line_k, line_{k+1}) = midpoint value
    interval_upper = max(
        (mags[i] + mags[i + 1]) / 2.0 + margin for i in range(grid_points - 1)
    )
    return BandBounds(
        f_lo=f_lo,
        f_hi=f_hi,
        grid_points=grid_points,
        grid_spacing_hz=df,
        max_magnitude_grid=max(mags),
        min_magnitude_grid=min(mags),
        between_grid_margin=margin,
        upper_bound=interval_upper,
        lower_bound=max(0.0, min(mags) - margin),
    )
