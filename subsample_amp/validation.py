"""Applicability conditions for the Poisson-subsampling amplification formula.

The only identity implemented in this package is

    eps' = log(1 + q * (exp(eps) - 1))

which is the standard amplification-by-subsampling bound valid under ALL of
the following assumptions:

  1. The base mechanism is *pure* eps-DP (delta = 0), with eps >= 0.
  2. Subsampling is *Poisson*: every record is kept independently with
     probability q, 0 <= q <= 1.
  3. The adjacency notion is add/remove (one record added or removed).

The formula is NOT valid for, e.g., uniform sampling without replacement
(fixed-size subsets), base mechanisms that are only (eps, delta)-DP with
delta > 0, or other adjacency/threat models.  Any parameter set that does
not satisfy the stated model is REJECTED by this module instead of being
silently plugged into the formula.

Scope and honesty notes:
  * This package only ever handles synthetic parameters; no real data.
  * A numerically accurate evaluation of the formula is evidence about
    arithmetic only -- it is not, by itself, a privacy proof.
  * Statistical experiments are not formal privacy proofs either.
"""

from __future__ import annotations

import enum
import math


class ApplicabilityError(ValueError):
    """Raised when the amplification formula does not apply."""


class SamplingModel(enum.Enum):
    """Subsampling models known to this package.

    Only POISSON is supported, because the implemented identity is exactly
    the Poisson-subsampling amplification bound.  Any other model must be
    rejected rather than silently mapped onto the formula.
    """

    POISSON = "poisson"


def _as_finite_float(name: str, value: object) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ApplicabilityError(
            f"{name} must be a real number, got {type(value).__name__}"
        )
    result = float(value)
    if math.isnan(result) or math.isinf(result):
        raise ApplicabilityError(f"{name} must be finite, got {result!r}")
    return result


def _normalize_model(model: object) -> SamplingModel:
    if isinstance(model, SamplingModel):
        normalized = model
    elif isinstance(model, str):
        try:
            normalized = SamplingModel(model)
        except ValueError:
            raise ApplicabilityError(
                f"unsupported sampling model {model!r}: the formula "
                "eps' = log(1 + q*(exp(eps) - 1)) only applies to Poisson "
                "subsampling"
            ) from None
    else:
        raise ApplicabilityError(
            f"unsupported sampling model {model!r}: expected SamplingModel.POISSON"
        )
    if normalized is not SamplingModel.POISSON:
        raise ApplicabilityError(
            f"sampling model {normalized.value!r} is not covered by the "
            "implemented amplification formula"
        )
    return normalized


def validate_subsample_applicability(
    eps: object,
    q: object,
    *,
    model: object = SamplingModel.POISSON,
    base_delta: object = 0.0,
) -> tuple[float, float]:
    """Validate that eps' = log(1 + q*(exp(eps) - 1)) applies.

    Returns the normalized ``(eps, q)`` float pair.  Raises
    :class:`ApplicabilityError` if any applicability condition is violated.
    """
    _normalize_model(model)
    eps_f = _as_finite_float("eps", eps)
    q_f = _as_finite_float("q", q)
    delta_f = _as_finite_float("base_delta", base_delta)
    if eps_f < 0.0:
        raise ApplicabilityError(f"eps must be >= 0, got {eps_f!r}")
    if not 0.0 <= q_f <= 1.0:
        raise ApplicabilityError(f"q must be in [0, 1], got {q_f!r}")
    if delta_f != 0.0:
        raise ApplicabilityError(
            "the formula requires a pure eps-DP base mechanism "
            f"(delta = 0), got delta = {delta_f!r}"
        )
    return eps_f, q_f


def validate_base_epsilon(
    eps: object,
    *,
    model: object = SamplingModel.POISSON,
    base_delta: object = 0.0,
) -> float:
    """Validate the base epsilon alone (used by the inversion routine)."""
    eps_f, _ = validate_subsample_applicability(
        eps, 0.0, model=model, base_delta=base_delta
    )
    return eps_f


def validate_target(target: object) -> float:
    """Validate a target amplified budget (must be finite and >= 0)."""
    target_f = _as_finite_float("target", target)
    if target_f < 0.0:
        raise ApplicabilityError(f"target budget must be >= 0, got {target_f!r}")
    return target_f
