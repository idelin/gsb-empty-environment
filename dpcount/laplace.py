"""Continuous Laplace mechanism for subject-level counting.

.. warning::
   This is the classic *continuous* Laplace mechanism implemented with
   IEEE-754 floats.  It is provided as the familiar baseline interface and is
   explicitly marked as a **numerical approximation**: float rounding and
   truncated tails mean the realised distribution is not exactly Laplace, so
   the epsilon guarantee is only approximate.  For an exactly verified
   mechanism on a finite integer domain use :mod:`dpcount.discrete`.

Definitions
-----------
* Query: count of records under subject-level adjacency (add/remove one
  subject).  ``sensitivity`` is the maximum number of records a single
  subject may contribute, i.e. the L1 sensitivity of the count.
* Noise: ``Laplace(0, b)`` with ``b = sensitivity / epsilon``, drawn by
  inversion: for ``U ~ Uniform(0, 1)``,
  ``F^{-1}(U) = -b * sign(U - 1/2) * ln(1 - 2 * |U - 1/2|)``.

The random source is injectable (any zero-argument callable returning a float
in ``(0, 1)``), which makes the inverse-CDF mapping reproducible: the same
uniform input always yields the same noise.  Negative noise is *kept* — the
raw noisy count may go below zero and is recorded as-is next to the
separately post-processed value; nothing is silently resampled.
"""

from __future__ import annotations

import math
import random
from dataclasses import dataclass, field
from typing import Callable, Optional


@dataclass(frozen=True)
class CountResult:
    """Raw and post-processed results are recorded separately."""

    mechanism: str
    epsilon: float
    sensitivity: int
    true_count: int
    raw: float
    postprocessed: float
    exact: bool = False  # continuous Laplace is a numerical approximation
    notes: tuple = field(default_factory=tuple)


class LaplaceMechanism:
    """Continuous Laplace noise for a counting query (float, approximate)."""

    def __init__(
        self,
        sensitivity: int,
        epsilon: float,
        uniform: Optional[Callable[[], float]] = None,
        postprocess: Optional[Callable[[float], float]] = None,
    ) -> None:
        if epsilon <= 0:
            raise ValueError("epsilon must be > 0")
        if sensitivity <= 0:
            raise ValueError("sensitivity must be a positive integer")
        self.sensitivity = int(sensitivity)
        self.epsilon = float(epsilon)
        self.scale = self.sensitivity / self.epsilon
        self._uniform = uniform if uniform is not None else random.random
        # Default post-processing: clamp the raw value to the feasible range
        # of a count.  Recorded separately from the raw value.
        self._postprocess = postprocess if postprocess is not None else (lambda v: max(0.0, v))

    def inverse_cdf(self, u: float) -> float:
        """Deterministic inverse CDF; reproducible for a fixed input ``u``."""
        if not 0.0 < u < 1.0:
            raise ValueError("u must lie in the open interval (0, 1)")
        t = u - 0.5
        return -self.scale * math.copysign(1.0, t) * math.log(1.0 - 2.0 * abs(t))

    def sample_noise(self) -> float:
        """Draw exactly one uniform and map it through the inverse CDF.

        The result is returned unchanged even when negative — no resampling.
        """
        return self.inverse_cdf(self._uniform())

    def noisy_count(self, true_count: int) -> CountResult:
        noise = self.sample_noise()
        raw = true_count + noise
        return CountResult(
            mechanism="continuous-laplace",
            epsilon=self.epsilon,
            sensitivity=self.sensitivity,
            true_count=int(true_count),
            raw=raw,
            postprocessed=self._postprocess(raw),
            exact=False,
            notes=("numerical approximation (float); see dpcount.discrete "
                   "for the exactly verified finite-domain mechanism",),
        )
