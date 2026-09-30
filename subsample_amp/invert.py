"""Inversion: largest sampling rate q meeting a target amplified budget.

For fixed eps > 0 the amplifier amp(q) = log(1 + q*(e^eps - 1)) is strictly
increasing in q on [0, 1] with amp(0) = 0 and amp(1) = eps.  Hence, for
0 <= target <= eps, the exact solution of amp(q) = target is

    q* = expm1(target) / expm1(eps)

evaluated in the log domain, log(q*) = log(expm1(target)) - log(expm1(eps))
with log(expm1(x)) = x + log(1 - e^-x) for large x, using directed-rounding
interval arithmetic throughout.  Even eps = 1000 (where exp(eps) overflows
a float) is handled without any "overflow then clip" step.

Residual evidence: the stable float amplifier and the interval amplifier
are re-evaluated at both bracket endpoints, confirming
amp(q_lo) <= target <= amp(q_hi).  This is arithmetic evidence about the
computed bracket, not a privacy proof.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from decimal import Decimal

from .core import subsampled_epsilon
from .dag import amp_interval
from .intervals import Interval
from .validation import (
    SamplingModel,
    validate_base_epsilon,
    validate_target,
)

_ZERO = Decimal(0)
_ONE = Decimal(1)

# Mirror of core._OVERFLOW_GUARD.
_FORM_SWITCH = 700.0


def _ln_expm1_iv(x: float) -> Interval:
    """Outward-rounded interval for log(expm1(x)), x > 0, overflow-free."""
    x_iv = Interval(x)
    if x <= _FORM_SWITCH:
        return x_iv.expm1().ln()
    # log(e^x - 1) = x + log(1 - e^-x); e^-x underflows gracefully to 0.
    return x_iv + (Interval(1) - (-x_iv).exp()).ln()


@dataclass(frozen=True)
class MaxQResult:
    """Result of inverting the amplification formula for a target budget."""

    eps: float
    target: float
    q_lo: float                 # float lower bound of q* (rounded outward)
    q_hi: float                 # float upper bound of q* (rounded outward)
    q_interval: Interval        # rigorous Decimal bracket of q*
    amp_at_q_lo: float          # stable float amp at q_lo
    amp_at_q_hi: float          # stable float amp at q_hi
    residual_lo: float          # amp_at_q_lo - target (<= 0 expected)
    residual_hi: float          # amp_at_q_hi - target (>= 0 expected)
    interval_contains_target: bool
    bracket_verified: bool
    notes: tuple[str, ...] = field(default_factory=tuple)

    def evidence(self) -> str:
        lines = [
            f"eps={self.eps!r}, target={self.target!r}",
            f"q* in [{self.q_interval.lo}, {self.q_interval.hi}] (Decimal, directed rounding)",
            f"float bracket: [{self.q_lo!r}, {self.q_hi!r}]",
            f"amp(q_lo) - target = {self.residual_lo!r} (must be <= 0)",
            f"amp(q_hi) - target = {self.residual_hi!r} (must be >= 0)",
            f"interval amp([q_lo, q_hi]) contains target: {self.interval_contains_target}",
            f"bracket verified: {self.bracket_verified}",
        ]
        lines.extend(f"note: {note}" for note in self.notes)
        return "\n".join(lines)


def _float_bracket(q_iv: Interval) -> tuple[float, float]:
    q_lo = math.nextafter(float(q_iv.lo), -math.inf)
    q_hi = math.nextafter(float(q_iv.hi), math.inf)
    return max(q_lo, 0.0), min(q_hi, 1.0)


def _boundary_result(
    eps: float, target: float, q: float, note: str
) -> MaxQResult:
    amp = subsampled_epsilon(eps, q)
    q_iv = Interval(q)
    amp_iv = amp_interval(Interval(eps), q_iv)
    residual = amp - target
    contains = amp_iv.contains(Decimal(target))
    return MaxQResult(
        eps=eps,
        target=target,
        q_lo=q,
        q_hi=q,
        q_interval=q_iv,
        amp_at_q_lo=amp,
        amp_at_q_hi=amp,
        residual_lo=residual,
        residual_hi=residual,
        interval_contains_target=contains,
        bracket_verified=(residual <= 0.0),
        notes=(note,),
    )


def max_q_for_budget(
    eps: float,
    target: float,
    *,
    model: SamplingModel = SamplingModel.POISSON,
    base_delta: float = 0.0,
) -> MaxQResult:
    """Largest q with log(1 + q*(exp(eps) - 1)) <= target, with proof bracket.

    Raises ApplicabilityError if the amplification formula does not apply
    (wrong sampling model, delta > 0, negative eps, non-finite input, ...).
    """
    eps = validate_base_epsilon(eps, model=model, base_delta=base_delta)
    target = validate_target(target)

    if eps == 0.0:
        return _boundary_result(
            eps, target, 1.0,
            "eps = 0: amplifier is identically 0 <= target for every q; max q = 1",
        )
    if target == 0.0:
        return _boundary_result(
            eps, target, 0.0, "target = 0 forces q = 0 (amp is strictly increasing)"
        )
    if target >= eps:
        return _boundary_result(
            eps, target, 1.0,
            "target >= eps = amp(1): the boundary q = 1 is feasible and maximal",
        )

    # 0 < target < eps: q* = expm1(target) / expm1(eps) in (0, 1), computed
    # in the log domain so arbitrarily large eps cannot overflow.
    q_iv = (_ln_expm1_iv(target) - _ln_expm1_iv(eps)).exp()
    q_iv = q_iv.intersect(_ZERO, _ONE)

    q_lo, q_hi = _float_bracket(q_iv)
    amp_lo = subsampled_epsilon(eps, q_lo)
    amp_hi = subsampled_epsilon(eps, q_hi)
    residual_lo = amp_lo - target
    residual_hi = amp_hi - target

    amp_iv = amp_interval(Interval(eps), q_iv)
    contains = amp_iv.contains(Decimal(target))

    notes: list[str] = []
    if q_iv.hi < Decimal(2) ** -1074:
        notes.append(
            "q* is below the smallest float subnormal; the float bracket "
            "degenerates to [0, 5e-324] -- use the Decimal interval instead"
        )
    # Rigorous part of the evidence: the directed-rounding Decimal bracket
    # lies in [0, 1] and the interval amplifier maps it onto the target.
    # The float residuals above are heuristic evidence (subject to float
    # rounding of the evaluation, typically a few ulps).
    verified = _ZERO <= q_iv.lo <= q_iv.hi <= _ONE and contains
    return MaxQResult(
        eps=eps,
        target=target,
        q_lo=q_lo,
        q_hi=q_hi,
        q_interval=q_iv,
        amp_at_q_lo=amp_lo,
        amp_at_q_hi=amp_hi,
        residual_lo=residual_lo,
        residual_hi=residual_hi,
        interval_contains_target=contains,
        bracket_verified=verified,
        notes=tuple(notes),
    )
