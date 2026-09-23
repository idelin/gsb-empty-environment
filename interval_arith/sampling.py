"""Rational sampling: counterexample search ONLY.

A sample that stays inside a claimed bound proves nothing; only the
certificate (exact rational re-derivation, see ``certificate.py``) proves
inclusion.  A sample outside the claimed bound is a genuine
counterexample, because both the sample point and the function
evaluation are exact rationals.
"""

from __future__ import annotations

from fractions import Fraction

from .core import Interval


def rational_samples(domain: Interval, n: int) -> list:
    """n+1 evenly spaced exact rational points on a bounded interval."""
    if not domain.is_bounded():
        raise ValueError("sampling requires a bounded domain")
    step = (domain.hi - domain.lo) / n
    return [domain.lo + step * i for i in range(n + 1)]


def find_counterexample(fn, domain: Interval, claimed: Interval, n: int = 32):
    """Return an exact rational x with fn(x) outside ``claimed``, or None.

    None does NOT prove inclusion -- it only means no counterexample was
    found among the samples.
    """
    for x in rational_samples(domain, n):
        if not claimed.contains(fn(x)):
            return x
    return None
