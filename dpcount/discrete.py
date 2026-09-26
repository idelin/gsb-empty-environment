"""Discrete Laplace counting mechanism on a finite integer domain.

Mechanism
---------
The mechanism is the *discrete Laplace* (two-sided geometric) distribution
on the integers, clamped to the finite output domain ``{0, ..., max_count}``:

* Unclamped: ``P(Y = y | c) = (1 - r)/(1 + r) * r ** |y - c|`` for ``y`` in
  the integers, where the base ``r`` is an *exact rational* satisfying
  ``r >= exp(-epsilon / sensitivity)``.
* Clamped: outputs below 0 are mapped to 0 and outputs above ``max_count``
  to ``max_count``.  Clamping is deterministic post-processing, so the pure
  epsilon-DP guarantee of the unclamped mechanism is preserved.

All probabilities on the finite domain are *exact rationals*
(:class:`fractions.Fraction`): the clamped tail masses are geometric series
with closed-form rational sums, e.g. ``P(0 | c) = r ** c / (1 + r)``.  Every
output in the domain has a strictly positive, exactly representable
probability -- floating-point tails can never turn a legal output into a
zero-probability event.

Subject-level sensitivity
-------------------------
Adjacency is add/remove-one-subject.  ``sensitivity`` (Delta) is the maximum
number of records one subject may contribute, i.e. the amount the true count
can change between adjacent datasets.  For the unclamped distribution, for
any output ``y`` and adjacent counts ``c, c'`` with ``|c - c'| = d``:

    P(y | c') / P(y | c) = r ** (|y - c'| - |y - c|) <= r ** -d

by the triangle inequality (the normaliser ``(1 - r)/(1 + r)`` does not
depend on ``c`` and cancels).  Hence with ``d <= Delta``:

    P(y | c') / P(y | c) <= r ** -Delta <= exp(epsilon)

where the last inequality holds because ``r >= exp(-epsilon / Delta)`` by
construction (see :func:`dpcount.exact.exp_neg_upper_bound`).  Post-processing
(clamping) preserves this bound, and :meth:`verify_dp` re-checks it
*exhaustively* on the finite domain with exact rational arithmetic.

Sampling
--------
Probabilities share the integer denominator ``Z = sum_y w_y`` with integer
weights derived below.  Sampling is exact inverse-CDF on integers: draw
``k`` uniformly from ``{0, ..., Z-1}`` via an injectable ``randbelow``
source and walk the cumulative weights.  No floats anywhere.

Formal verification
-------------------
:meth:`verify_dp` *exhaustively enumerates* every adjacent pair of counts in
the finite domain and every output, and checks the probability-ratio bound
with exact rational comparisons.  This is a finite-domain formal check, not
a statistical experiment; sampling frequencies are never used as evidence.
"""

from __future__ import annotations

import math
import secrets
from dataclasses import dataclass
from fractions import Fraction
from typing import Callable, List, Optional

from .exact import exp_neg_upper_bound, DEFAULT_EXP_TOL


@dataclass(frozen=True)
class VerificationResult:
    """Outcome of the exhaustive exact DP check."""

    ok: bool
    max_ratio: Fraction          # largest observed P(y|c')/P(y|c) over all pairs/outputs
    bound: Fraction              # claimed per-pair bound: claimed_r ** -stride
    stride: int                  # adjacency stride (sensitivity) that was checked
    pairs_checked: int
    outputs_checked: int
    counterexample: Optional[tuple] = None  # (c, c', y, ratio) if a violation was found


class DiscreteLaplaceMechanism:
    """Exact rational discrete Laplace, clamped to {0..max_count}."""

    def __init__(
        self,
        max_count: int,
        sensitivity: int,
        epsilon,
        randbelow: Optional[Callable[[int], int]] = None,
        exp_tol: Fraction = DEFAULT_EXP_TOL,
    ) -> None:
        epsilon = Fraction(epsilon)
        if epsilon <= 0:
            raise ValueError("epsilon must be > 0")
        if sensitivity <= 0:
            raise ValueError("sensitivity must be a positive integer")
        if max_count <= 0:
            raise ValueError("max_count must be a positive integer")
        self.max_count = int(max_count)
        self.sensitivity = int(sensitivity)
        self.epsilon = epsilon
        # Exact rational base r >= exp(-epsilon / sensitivity); the mechanism
        # is *defined* with this r, so the DP bound r**-sensitivity <=
        # exp(epsilon) is conservative and provable in rational arithmetic.
        self.r = exp_neg_upper_bound(epsilon / self.sensitivity, exp_tol)
        self._randbelow = randbelow if randbelow is not None else secrets.randbelow
        self._weights_cache = {}

    @classmethod
    def from_base_r(cls, max_count: int, sensitivity: int, r,
                    randbelow: Optional[Callable[[int], int]] = None
                    ) -> "DiscreteLaplaceMechanism":
        """Define the mechanism directly from a rational base r in (0, 1).

        Useful when an exact, small rational base is wanted (e.g. r = 1/2).
        The privacy guarantee is epsilon = sensitivity * ln(1/r); epsilon is
        reported as a float for information only -- all guarantees are
        checked against the exact rational r.
        """
        r = Fraction(r)
        if not 0 < r < 1:
            raise ValueError("r must be a rational in the open interval (0, 1)")
        self = cls.__new__(cls)
        self.max_count = int(max_count)
        self.sensitivity = int(sensitivity)
        if self.max_count <= 0 or self.sensitivity <= 0:
            raise ValueError("max_count and sensitivity must be positive integers")
        self.r = r
        self.epsilon = self.sensitivity * math.log(1 / float(r))  # informational
        self._randbelow = randbelow if randbelow is not None else secrets.randbelow
        self._weights_cache = {}
        return self

    # ------------------------------------------------------------------
    # Exact distribution
    # ------------------------------------------------------------------
    def weights(self, true_count: int) -> List[int]:
        """Unnormalised *integer* weights w_y with P(y) = w_y / sum(w).

        With r = a/b and M = max(c, max_count - c):
          * clamped low end:  w_0 = a**c * b**(M - c)            (mass r**c/(1+r))
          * clamped high end: w_N = a**(N-c) * b**(M - (N-c))    (mass r**(N-c)/(1+r))
          * interior:         w_y = (b-a) * a**|y-c| * b**(M-1-|y-c|)
        """
        self._check_count(true_count)
        cached = self._weights_cache.get(true_count)
        if cached is not None:
            return cached
        a, b = self.r.numerator, self.r.denominator
        n = self.max_count
        c = true_count
        max_dist = max(c, n - c)
        w = [0] * (n + 1)
        w[0] = a ** c * b ** (max_dist - c)
        w[n] = a ** (n - c) * b ** (max_dist - (n - c))
        for y in range(1, n):
            d = abs(y - c)
            w[y] = (b - a) * a ** d * b ** (max_dist - 1 - d)
        self._weights_cache[true_count] = w
        return w

    def probabilities(self, true_count: int) -> List[Fraction]:
        """Exact output distribution as Fractions; sums to exactly 1."""
        w = self.weights(true_count)
        total = sum(w)
        return [Fraction(wy, total) for wy in w]

    # ------------------------------------------------------------------
    # Exact sampling (inverse CDF on integers)
    # ------------------------------------------------------------------
    def inverse_cdf(self, true_count: int, k: int) -> int:
        """Deterministic inverse-CDF map: k in [0, Z) -> output y.

        Reproducible: the same integer ``k`` always yields the same output.
        """
        w = self.weights(true_count)
        total = sum(w)
        if not 0 <= k < total:
            raise ValueError(f"k must lie in [0, {total})")
        cumulative = 0
        for y, wy in enumerate(w):
            cumulative += wy
            if k < cumulative:
                return y
        raise AssertionError("unreachable: k < sum(weights)")

    def sample(self, true_count: int) -> int:
        """Draw one unbiased sample using the injected random source."""
        total = sum(self.weights(true_count))
        return self.inverse_cdf(true_count, self._randbelow(total))

    # ------------------------------------------------------------------
    # Exhaustive formal verification (exact rational arithmetic)
    # ------------------------------------------------------------------
    def verify_dp(self, stride: Optional[int] = None,
                  claimed_r: Optional[Fraction] = None) -> VerificationResult:
        """Exhaustively check the ratio bound over all adjacencies/outputs.

        For every ordered pair of counts ``(c, c')`` with
        ``1 <= |c - c'| <= stride`` and every output ``y`` in the domain,
        checks ``P(y|c') <= P(y|c) * claimed_r ** -|c-c'|`` with exact
        Fraction comparisons.  Defaults: ``stride = sensitivity`` and
        ``claimed_r = self.r`` (so the bound implies epsilon-DP).
        """
        stride = self.sensitivity if stride is None else int(stride)
        claimed = self.r if claimed_r is None else Fraction(claimed_r)
        bound = claimed ** -stride
        max_ratio = Fraction(0)
        pairs = 0
        outputs = 0
        for c in range(self.max_count + 1):
            p_c = self.probabilities(c)
            lo = max(0, c - stride)
            hi = min(self.max_count, c + stride)
            for c2 in range(lo, hi + 1):
                if c2 == c:
                    continue
                d = abs(c2 - c)
                local_bound = claimed ** -d
                p_c2 = self.probabilities(c2)
                pairs += 1
                for y in range(self.max_count + 1):
                    outputs += 1
                    ratio = p_c2[y] / p_c[y]
                    if ratio > max_ratio:
                        max_ratio = ratio
                    if ratio > local_bound:
                        return VerificationResult(
                            ok=False, max_ratio=max_ratio, bound=bound,
                            stride=stride, pairs_checked=pairs,
                            outputs_checked=outputs,
                            counterexample=(c, c2, y, ratio))
        return VerificationResult(
            ok=True, max_ratio=max_ratio, bound=bound, stride=stride,
            pairs_checked=pairs, outputs_checked=outputs)

    # ------------------------------------------------------------------
    def privacy_loss_bound(self) -> Fraction:
        """Exact rational upper bound on the per-adjacency probability ratio."""
        return self.r ** -self.sensitivity

    def _check_count(self, true_count: int) -> None:
        if not 0 <= true_count <= self.max_count:
            raise ValueError(
                f"true_count must lie in [0, {self.max_count}]")
