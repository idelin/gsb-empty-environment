"""Stable evaluation, interval certification and inversion of the
subsampled-amplification formula eps' = log(1 + q*(exp(eps) - 1)).

Synthetic parameters only; numerical accuracy is not a privacy proof.
"""

from . import assumptions, dag, intervals, invert, reference, stable

__all__ = ["assumptions", "dag", "intervals", "invert", "reference", "stable"]
