"""Exact reference values for finite discrete domains."""

from __future__ import annotations

from typing import Any, Callable, Hashable


def exact_expectation(target: Any, f: Callable[[Hashable], float]) -> float:
    """EXACT reference: E_p[f] computed by summation over the finite support.

    This is a closed-form value, not a Monte Carlo estimate; use it as the
    ground truth when labeling empirical estimates.
    """
    if not hasattr(target, "support"):
        raise TypeError("exact expectation requires a finite-support distribution")
    return sum(target.pdf(x) * float(f(x)) for x in target.support())
