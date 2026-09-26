"""Exact rational reference implementation (fractions.Fraction).

Used to cross-check the float/log-domain updater on small models.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Iterable, Mapping

from .updater import ConflictError


def _frac(x) -> Fraction:
    if isinstance(x, Fraction):
        return x
    if isinstance(x, float):
        return Fraction(str(x))
    return Fraction(x)


def exact_evidence(priors: Mapping, observations: Iterable[Mapping]) -> Fraction:
    """P(observations) under the prior, as an exact rational."""
    weights = {h: _frac(p) for h, p in priors.items()}
    prior_total = sum(weights.values())
    weights = {h: w / prior_total for h, w in weights.items()}
    for obs in observations:
        for h in weights:
            weights[h] *= _frac(obs.get(h, 1))
    return sum(weights.values())


def exact_posterior(priors: Mapping, observations: Iterable[Mapping]) -> dict:
    """Normalized posterior as exact rationals; raises on zero evidence."""
    observations = list(observations)
    z = exact_evidence(priors, observations)
    if z == 0:
        raise ConflictError("total evidence is zero")
    weights = {h: _frac(p) for h, p in priors.items()}
    prior_total = sum(weights.values())
    weights = {h: w / prior_total for h, w in weights.items()}
    for obs in observations:
        for h in weights:
            weights[h] *= _frac(obs.get(h, 1))
    return {h: w / z for h, w in weights.items()}
