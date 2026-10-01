"""Exact rounding of rational values to integers.

All policies operate directly on ``fractions.Fraction`` (or any
``numbers.Rational``) so no float conversion ever takes place.
"""

from __future__ import annotations

import enum
from fractions import Fraction
from numbers import Rational


class RoundingPolicy(enum.Enum):
    """How a rational value is rounded to an integer."""

    HALF_EVEN = "half_even"      # ties to even (banker's rounding)
    HALF_AWAY = "half_away"      # ties away from zero
    HALF_UP = "half_up"          # ties toward +infinity
    FLOOR = "floor"              # toward -infinity
    CEIL = "ceil"                # toward +infinity
    TRUNC = "trunc"              # toward zero


def _as_fraction(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, Rational):
        return Fraction(value.numerator, value.denominator)
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"expected a rational value, got {type(value).__name__}")


def round_rational(value, policy: RoundingPolicy = RoundingPolicy.HALF_EVEN) -> int:
    """Round ``value`` to an integer under ``policy`` without floats.

    Correct for negative inputs in every policy.
    """
    fr = _as_fraction(value)
    floor = fr.numerator // fr.denominator  # exact floor, works for negatives
    rem = fr - floor                        # 0 <= rem < 1

    if policy is RoundingPolicy.FLOOR:
        return floor
    if policy is RoundingPolicy.CEIL:
        return floor if rem == 0 else floor + 1
    if policy is RoundingPolicy.TRUNC:
        return floor if fr >= 0 else (floor if rem == 0 else floor + 1)

    half = Fraction(1, 2)
    if rem < half:
        return floor
    if rem > half:
        return floor + 1
    # exact tie
    if policy is RoundingPolicy.HALF_EVEN:
        return floor if floor % 2 == 0 else floor + 1
    if policy is RoundingPolicy.HALF_AWAY:
        return floor + 1 if fr >= 0 else floor
    if policy is RoundingPolicy.HALF_UP:
        return floor + 1
    raise ValueError(f"unknown rounding policy: {policy}")
