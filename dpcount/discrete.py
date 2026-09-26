"""Discrete Laplace (two-sided geometric) count mechanism on a finite domain.

Mechanism (all arithmetic exact, via ``fractions.Fraction``)
----------------------------------------------------------------
For true count ``c`` and finite integer output domain ``X = [lo, hi]``::

    Pr[M(c) = x] = p ** |x - c| / Z(c),   Z(c) = sum_{y in X} p ** |y - c|

where ``p`` is an exact rational with ``0 < p < 1`` ("decay").  This is the
truncated two-sided geometric / discrete Laplace distribution; probabilities
are exact rational numbers, so every output in X has strictly positive
probability -- there is no floating-point tail that could make a
positive-probability event impossible.

Sampling is exact and unbiased: a uniform real in [0, 1) is built lazily from
fair random bits as a dyadic interval, and compared exactly against rational
CDF cells.  The interval is only refined until it lies wholly inside one CDF
cell, so the output has exactly the distribution above (no fixed-precision
rounding, no rejection/redrawing of negative values).

``inverse_cdf(count, u)`` takes an explicit rational uniform input ``u`` and
returns the output, so any deterministic random input can be replayed/recomputed
by hand.

Subject sensitivity
--------------------
The query is "count of subjects satisfying a predicate".  Adding or removing
one subject changes the count by at most 1, so the subject sensitivity is
Delta = 1; the class accepts an explicit positive integer sensitivity for
general subject-level counting queries.

Formal privacy check
--------------------
``verify_dp`` enumerates EVERY pair of counts in the finite adjacency domain
with 1 <= |c - c'| <= Delta and EVERY output x in X, computing the exact
rational ratio Pr[M(c)=x] / Pr[M(c')=x].  This is a formal exhaustive proof
of the epsilon bound (epsilon = ln of the exact maximum ratio), NOT a
sampling-frequency experiment.

Note on truncation: on a finite domain the normalizers Z(c) differ, so the
naive bound p ** -Delta need not hold near the boundary.  verify_dp computes
the exact tight maximum ratio; use it rather than the naive bound.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from typing import List, Optional, Tuple


class ParameterError(ValueError):
    """Invalid mechanism parameter, e.g. epsilon <= 0 or decay not in (0, 1)."""


@dataclass(frozen=True)
class NoisyCountRecord:
    """Raw mechanism output and its post-processing, kept separate.

    Negative raw outputs are kept as-is; post-processing (e.g. clamping to a
    non-negative count) never triggers resampling.
    """

    mechanism: str
    true_count: int
    raw: int
    postprocessed: int
    postprocessing: str


def rational_decay_for_epsilon(
    epsilon: float, sensitivity: int, precision_bits: int = 48
) -> Fraction:
    """Return an exact rational decay ``p`` targeting a privacy budget.

    ``p`` is the dyadic rational with denominator ``2 ** precision_bits`` that
    is >= exp(-epsilon/sensitivity) (rounded UP, the privacy-conservative
    direction: larger p spreads more mass, i.e. adds more noise).

    epsilon <= 0 is rejected.  This helper uses a floating-point estimate to
    CHOOSE p; the formal privacy guarantee comes only from ``verify_dp`` on the
    resulting exact rational mechanism.
    """
    if not epsilon > 0:
        raise ParameterError("epsilon must be > 0 (got %r)" % (epsilon,))
    if isinstance(sensitivity, bool) or not isinstance(sensitivity, int):
        raise ParameterError("sensitivity must be an int")
    if sensitivity <= 0:
        raise ParameterError("sensitivity must be > 0")
    if precision_bits < 1:
        raise ParameterError("precision_bits must be >= 1")
    target = math.exp(-epsilon / sensitivity)
    denominator = 1 << precision_bits
    numerator = math.ceil(target * denominator)
    if numerator <= 0:
        raise ParameterError("epsilon too large for requested precision")
    if numerator >= denominator:
        raise ParameterError("epsilon too small for requested precision")
    return Fraction(numerator, denominator)


class DiscreteLaplaceCount:
    """Exact rational truncated two-sided geometric noise for a count."""

    def __init__(self, sensitivity: int, decay: Fraction, lo: int, hi: int):
        if isinstance(sensitivity, bool) or not isinstance(sensitivity, int):
            raise ParameterError("sensitivity must be an int")
        if sensitivity <= 0:
            raise ParameterError("sensitivity must be > 0")
        decay = Fraction(decay)
        if not (Fraction(0) < decay < Fraction(1)):
            raise ParameterError("decay p must satisfy 0 < p < 1")
        if lo > hi:
            raise ParameterError("empty output domain: lo > hi")
        self.sensitivity = sensitivity
        self.decay = decay
        self.lo = lo
        self.hi = hi
        self._normalizers: dict[int, Fraction] = {}

    # ----- exact distribution -------------------------------------------------

    def normalizer(self, count: int) -> Fraction:
        """Z(count) = sum over the output domain of p ** |x - count| (exact)."""
        if count not in self._normalizers:
            p = self.decay
            self._normalizers[count] = sum(
                (p ** abs(x - count) for x in range(self.lo, self.hi + 1)),
                Fraction(0),
            )
        return self._normalizers[count]

    def probability(self, x: int, count: int) -> Fraction:
        """Exact Pr[M(count) = x]; 0 outside the output domain."""
        if x < self.lo or x > self.hi:
            return Fraction(0)
        return self.decay ** abs(x - count) / self.normalizer(count)

    def cdf_cells(self, count: int) -> List[Tuple[int, Fraction, Fraction]]:
        """List of (output x, cdf_lo, cdf_hi) half-open cells partitioning [0,1)."""
        z = self.normalizer(count)
        p = self.decay
        cells = []
        cumulative = Fraction(0)
        for x in range(self.lo, self.hi + 1):
            mass = p ** abs(x - count) / z
            cells.append((x, cumulative, cumulative + mass))
            cumulative += mass
        return cells

    def inverse_cdf(self, count: int, u: Fraction) -> int:
        """Deterministic inverse CDF for explicit rational uniform input u in [0, 1).

        Returns the unique x with cdf(x-1) <= u < cdf(x).  Same rational input
        always yields the same integer output (recomputable by hand).
        """
        u = Fraction(u)
        if not (Fraction(0) <= u < Fraction(1)):
            raise ParameterError("uniform input u must lie in [0, 1)")
        for x, cell_lo, cell_hi in self.cdf_cells(count):
            if cell_lo <= u < cell_hi:
                return x
        raise AssertionError("inverse CDF failed: CDF cells do not cover [0, 1)")

    # ----- exact unbiased sampling -------------------------------------------

    def sample(self, count: int, rng) -> int:
        """Draw one output with EXACTLY the stated discrete distribution.

        ``rng`` is any object exposing ``getrandbits(k) -> int`` (e.g.
        ``random.Random``).  A uniform real u in [0,1) is represented as the
        dyadic interval [num/2**prec, (num+1)/2**prec); bits are added only
        until the interval lies wholly inside one CDF cell.  Comparisons are
        exact Fraction comparisons, so the procedure is unbiased and terminates
        almost surely.
        """
        cells = self.cdf_cells(count)
        numerator = 0
        precision = 0
        while True:
            interval_lo = Fraction(numerator, 1 << precision)
            interval_hi = Fraction(numerator + 1, 1 << precision)
            for x, cell_lo, cell_hi in cells:
                if interval_lo >= cell_lo and interval_hi <= cell_hi:
                    return x
            numerator = (numerator << 1) | (rng.getrandbits(1))
            precision += 1

    # ----- record keeping (raw vs post-processed) ----------------------------

    def noisy_count(self, count: int, rng, floor: int = 0) -> NoisyCountRecord:
        """Run the mechanism once.

        The raw integer output is recorded even when negative; post-processing
        clamps to ``floor`` (default 0) and is recorded separately.  Nothing is
        ever redrawn because the raw value was negative.
        """
        raw = self.sample(count, rng)
        postprocessed = max(floor, raw)
        return NoisyCountRecord(
            mechanism="discrete-laplace(exact-rational)",
            true_count=count,
            raw=raw,
            postprocessed=postprocessed,
            postprocessing=("raw kept as-is; postprocessed = max(%d, raw), "
                            "no resampling" % floor),
        )


@dataclass(frozen=True)
class VerificationReport:
    """Result of the exhaustive, exact rational DP ratio check."""

    sensitivity: int
    count_domain: Tuple[int, int]
    output_domain: Tuple[int, int]
    adjacent_pairs: int
    ratios_checked: int
    max_ratio: Fraction
    argmax: Tuple[int, int, int]
    verified_epsilon: float
    claimed_max_ratio: Optional[Fraction]
    claimed_bound_holds: Optional[bool]

    def __str__(self) -> str:
        lines = [
            "exhaustive exact-rational DP verification",
            "  adjacency domain counts c in [%d, %d], 1 <= |c-c'| <= %d"
            % (self.count_domain[0], self.count_domain[1], self.sensitivity),
            "  output domain x in [%d, %d]" % self.output_domain,
            "  ordered adjacent pairs checked: %d" % self.adjacent_pairs,
            "  exact ratios checked:          %d" % self.ratios_checked,
            "  max Pr[M(c)=x]/Pr[M(c')=x] = %s" % self.max_ratio,
            "  (= %.12f; Fraction above is the exact value)"
            % (self.max_ratio.numerator / self.max_ratio.denominator),
            "  attained at (c, c', x) = (%d, %d, %d)"
            % (self.argmax[0], self.argmax[1], self.argmax[2]),
            "  verified epsilon = ln(max ratio) = %.10f (float display of "
            "exact rational bound)" % self.verified_epsilon,
        ]
        if self.claimed_max_ratio is not None:
            lines.append(
                "  claimed bound %s holds: %s"
                % (self.claimed_max_ratio, self.claimed_bound_holds)
            )
        return "\n".join(lines)


def verify_dp(
    mechanism: DiscreteLaplaceCount,
    count_lo: int,
    count_hi: int,
    claimed_max_ratio: Optional[Fraction] = None,
) -> VerificationReport:
    """Exhaustively verify the DP ratio bound over a finite adjacency domain.

    Enumerates every ordered pair of counts c, c' with
    1 <= |c - c'| <= sensitivity and every output x, and computes the exact
    rational likelihood ratio.  This is a formal finite proof, not a
    statistical test.
    """
    if count_lo > count_hi:
        raise ParameterError("empty count adjacency domain")
    claimed = None if claimed_max_ratio is None else Fraction(claimed_max_ratio)
    max_ratio = Fraction(0)
    argmax = (count_lo, count_lo, mechanism.lo)
    pairs = 0
    ratios = 0
    for c in range(count_lo, count_hi + 1):
        for c_prime in range(count_lo, count_hi + 1):
            distance = abs(c - c_prime)
            if distance == 0 or distance > mechanism.sensitivity:
                continue
            pairs += 1
            for x in range(mechanism.lo, mechanism.hi + 1):
                numerator = mechanism.probability(x, c)
                denominator = mechanism.probability(x, c_prime)
                if denominator == 0:
                    if numerator != 0:
                        raise AssertionError(
                            "infinite ratio: output %d has zero probability "
                            "under c'=%d but positive under c=%d"
                            % (x, c_prime, c)
                        )
                    continue
                ratio = numerator / denominator
                ratios += 1
                if ratio > max_ratio:
                    max_ratio = ratio
                    argmax = (c, c_prime, x)
    bound_holds = None if claimed is None else (max_ratio <= claimed)
    return VerificationReport(
        sensitivity=mechanism.sensitivity,
        count_domain=(count_lo, count_hi),
        output_domain=(mechanism.lo, mechanism.hi),
        adjacent_pairs=pairs,
        ratios_checked=ratios,
        max_ratio=max_ratio,
        argmax=argmax,
        verified_epsilon=math.log(max_ratio.numerator / max_ratio.denominator),
        claimed_max_ratio=claimed,
        claimed_bound_holds=bound_holds,
    )
