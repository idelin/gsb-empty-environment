"""Exponential mechanism over legal candidate subsets.

Distribution (defined on legal subsets S, never on individual items):

    P(S) = exp(epsilon * u(S) / (2 * sensitivity)) / Z,
    Z    = sum over legal S' of exp(epsilon * u(S') / (2 * sensitivity))

with u(S) = sum of member integer scores.

Numerical policy
----------------
* All float64 weights go through a log-sum-exp stabilisation (subtract the
  max log-weight before exponentiating), so arbitrarily large
  epsilon * u / (2 * sensitivity) cannot overflow.
* A high-precision Decimal reference (default 50 significant digits) is
  provided for small domains. exp is transcendental, so this reference is
  itself an approximation, but its error is ~10**-prec, far below float64
  rounding (~1e-16 relative). The observed gap between the float64 and the
  Decimal result is reported explicitly as the numerical error.
* Degenerate rules (explicit, deterministic, no division by zero):
    - sensitivity == 0  ->  uniform over legal subsets
      (score differences are then uninformative by definition);
    - epsilon == 0      ->  uniform over legal subsets (falls out of the
      formula naturally, since every log-weight is 0).

Sampling is inverse-CDF over the enumerated legal subsets, driven by a
seeded random.Random instance. Every uniform variate actually consumed is
recorded in a SamplingEvidence object so each draw can be replayed and
audited bit-for-bit.
"""

from __future__ import annotations

import bisect
import math
import random
from dataclasses import dataclass
from decimal import Decimal, localcontext
from typing import List, Sequence, Tuple

from .model import (
    Candidate,
    Constraints,
    InfeasibleError,
    enumerate_legal_subsets,
    utility,
)

__all__ = [
    "InfeasibleError",
    "ExponentialMechanism",
    "SamplingEvidence",
    "DrawRecord",
]

DEFAULT_REFERENCE_PRECISION = 50


@dataclass(frozen=True)
class DrawRecord:
    """Audit record for one sampled draw."""

    draw_index: int
    uniform: float            # the raw variate consumed from the RNG
    subset_index: int         # index into the mechanism's legal-subset list
    subset_ids: Tuple[str, ...]
    subset_probability: float  # probability of that subset under the distribution


@dataclass(frozen=True)
class SamplingEvidence:
    """Replayable evidence of a sampling run."""

    seed: int
    epsilon: float
    sensitivity: float
    probabilities: Tuple[float, ...]      # full normalised distribution used
    subset_ids: Tuple[Tuple[str, ...], ...]  # id tuple of every legal subset
    draws: Tuple[DrawRecord, ...]

    def replay(self) -> bool:
        """Re-run the recorded seed and check every draw reproduces exactly."""
        rng = random.Random(self.seed)
        cumulative = _cumulative(self.probabilities)
        for record in self.draws:
            u = rng.random()
            if u != record.uniform:
                return False
            idx = bisect.bisect_right(cumulative, u)
            if idx != record.subset_index:
                return False
            if self.subset_ids[idx] != record.subset_ids:
                return False
        return True


def _cumulative(probabilities: Sequence[float]) -> List[float]:
    total = 0.0
    out = []
    for p in probabilities:
        total += p
        out.append(total)
    # Force the last cumulative point to exactly 1.0 so a uniform variate
    # can never fall off the end due to float64 rounding.
    out[-1] = 1.0
    return out


class ExponentialMechanism:
    """Exponential mechanism over the legal-subset domain."""

    def __init__(
        self,
        candidates: Sequence[Candidate],
        constraints: Constraints,
        epsilon: float,
        sensitivity: float,
        max_enumerated: int = 2_000_000,
    ) -> None:
        epsilon = float(epsilon)
        sensitivity = float(sensitivity)
        if not math.isfinite(epsilon) or epsilon < 0.0:
            raise ValueError("epsilon must be a finite float >= 0")
        if not math.isfinite(sensitivity) or sensitivity < 0.0:
            raise ValueError("sensitivity must be a finite float >= 0")
        self.epsilon = epsilon
        self.sensitivity = sensitivity
        self.candidates: Tuple[Candidate, ...] = tuple(candidates)
        self.constraints = constraints
        # Raises InfeasibleError when the domain is empty; never truncates.
        self.subsets = enumerate_legal_subsets(
            self.candidates, constraints, max_enumerated=max_enumerated
        )
        self.utilities: Tuple[int, ...] = tuple(utility(s) for s in self.subsets)
        self._log_weights: Tuple[float, ...] = tuple(
            self._log_weight(u) for u in self.utilities
        )

    # -- weights ---------------------------------------------------------

    def _log_weight(self, u: int) -> float:
        if self.sensitivity == 0.0:
            # Explicit deterministic rule: sensitivity 0 means scores carry
            # no information, so the distribution is uniform. No division
            # by zero is ever performed.
            return 0.0
        return self.epsilon * u / (2.0 * self.sensitivity)

    @property
    def log_weights(self) -> Tuple[float, ...]:
        return self._log_weights

    # -- probabilities ---------------------------------------------------

    def probabilities(self) -> List[float]:
        """Normalised float64 probabilities via log-sum-exp stabilisation."""
        m = max(self._log_weights)
        exps = [math.exp(w - m) for w in self._log_weights]
        z = math.fsum(exps)
        return [e / z for e in exps]

    def partition_function_shifted(self) -> float:
        """Stable partition function Z / exp(max log-weight)."""
        m = max(self._log_weights)
        return math.fsum(math.exp(w - m) for w in self._log_weights)

    def probabilities_decimal(
        self, precision: int = DEFAULT_REFERENCE_PRECISION
    ) -> List[Decimal]:
        """High-precision reference probabilities for small domains.

        Computed with Decimal at ``precision`` significant digits using the
        same max-subtraction stabilisation. Error is on the order of
        10**-precision, so this serves as the 'exact' reference against
        which the float64 numerical error is measured.
        """
        if precision < 10:
            raise ValueError("precision must be >= 10 to be a useful reference")
        with localcontext() as ctx:
            ctx.prec = precision
            if self.sensitivity == 0.0:
                log_weights = [Decimal(0)] * len(self.utilities)
            else:
                eps = Decimal(str(self.epsilon))
                sens = Decimal(str(self.sensitivity))
                denom = Decimal(2) * sens
                log_weights = [eps * Decimal(u) / denom for u in self.utilities]
            m = max(log_weights)
            exps = [(w - m).exp() for w in log_weights]
            z = sum(exps, Decimal(0))
            return [e / z for e in exps]

    def numerical_error(
        self, precision: int = DEFAULT_REFERENCE_PRECISION
    ) -> dict:
        """Explicit error of float64 probabilities vs the Decimal reference."""
        ref = self.probabilities_decimal(precision=precision)
        got = self.probabilities()
        max_abs = 0.0
        max_rel = 0.0
        for p, r in zip(got, ref):
            r_float = float(r)
            abs_err = abs(p - r_float)
            max_abs = max(max_abs, abs_err)
            if r_float > 0.0:
                max_rel = max(max_rel, abs_err / r_float)
        return {
            "precision": precision,
            "max_abs_error": max_abs,
            "max_rel_error": max_rel,
            "domain_size": len(self.subsets),
        }

    # -- sampling --------------------------------------------------------

    def sample(self, n: int = 1, seed: int = 0) -> SamplingEvidence:
        """Draw ``n`` independent samples with a fully auditable trail."""
        if isinstance(n, bool) or not isinstance(n, int) or n < 1:
            raise ValueError("n must be an integer >= 1")
        if isinstance(seed, bool) or not isinstance(seed, int):
            raise ValueError("seed must be an integer")
        probs = self.probabilities()
        cumulative = _cumulative(probs)
        subset_ids = tuple(tuple(c.cid for c in s) for s in self.subsets)
        rng = random.Random(seed)
        draws = []
        for i in range(n):
            u = rng.random()
            idx = bisect.bisect_right(cumulative, u)
            draws.append(
                DrawRecord(
                    draw_index=i,
                    uniform=u,
                    subset_index=idx,
                    subset_ids=subset_ids[idx],
                    subset_probability=probs[idx],
                )
            )
        return SamplingEvidence(
            seed=seed,
            epsilon=self.epsilon,
            sensitivity=self.sensitivity,
            probabilities=tuple(probs),
            subset_ids=subset_ids,
            draws=tuple(draws),
        )
