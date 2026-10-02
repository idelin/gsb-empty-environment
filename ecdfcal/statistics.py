"""Distribution-free statistics on ECDFs: frequencies, total variation, KS.

All arithmetic is exact (fractions.Fraction); no normal approximation anywhere.
"""
from __future__ import annotations

from fractions import Fraction

from .ecdf import ECDF


def frequency_table(ecdf: ECDF):
    """Rows of (value, multiplicity, mass, cumulative probability).

    The mass at a tied value already includes every duplicate observation,
    i.e. the CDF jumps once, by the full multiplicity.
    """
    return list(zip(ecdf.support, ecdf.multiplicities, ecdf.masses, ecdf.cumulative))


def ks_statistic(a: ECDF, b: ECDF) -> Fraction:
    """max_x |F_a(x) - F_b(x)|.

    Both ECDFs are right-continuous step functions whose only jumps are at
    the pooled support, so the supremum is attained at a support point.
    """
    best = Fraction(0)
    for value in sorted(set(a.support) | set(b.support)):
        diff = abs(a.cdf_at(value) - b.cdf_at(value))
        if diff > best:
            best = diff
    return best


def tv_distance(a: ECDF, b: ECDF) -> Fraction:
    """Total variation 1/2 * sum_x |p_a(x) - p_b(x)| over the pooled support."""
    mass_a = dict(zip(a.support, a.masses))
    mass_b = dict(zip(b.support, b.masses))
    zero = Fraction(0)
    total = sum(
        abs(mass_a.get(v, zero) - mass_b.get(v, zero))
        for v in set(a.support) | set(b.support)
    )
    return total / 2
