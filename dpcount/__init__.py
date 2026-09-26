"""Differentially private subject-level counting (synthetic data only).

Two mechanisms are provided:

* :class:`LaplaceMechanism` — the classic continuous Laplace mechanism
  (float arithmetic; explicitly marked as a numerical approximation).
* :class:`DiscreteLaplaceMechanism` — discrete Laplace on a finite integer
  domain with exact rational probabilities, unbiased integer sampling, and
  an exhaustive formal verification of the probability-ratio bound.
"""

from .laplace import CountResult, LaplaceMechanism
from .discrete import DiscreteLaplaceMechanism, VerificationResult
from .exact import exp_neg_upper_bound

__all__ = [
    "CountResult",
    "LaplaceMechanism",
    "DiscreteLaplaceMechanism",
    "VerificationResult",
    "exp_neg_upper_bound",
]
