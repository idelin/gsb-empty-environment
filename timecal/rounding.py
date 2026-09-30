"""Exact rounding of rational values; never touches floating point."""

from __future__ import annotations

from enum import Enum
from fractions import Fraction


class RoundingPolicy(Enum):
    HALF_EVEN = "half_even"        # nearest, ties to even (banker's rounding)
    HALF_AWAY = "half_away"        # nearest, ties away from zero
    FLOOR = "floor"                # toward -infinity
    CEILING = "ceiling"            # toward +infinity
    TOWARD_ZERO = "toward_zero"    # truncate


def round_fraction(value, policy: RoundingPolicy = RoundingPolicy.HALF_EVEN) -> int:
    """Round a rational to an integer exactly, correctly for negatives."""
    if not isinstance(value, Fraction):
        value = Fraction(value)
    num = value.numerator
    den = value.denominator
    floor_q = num // den            # exact mathematical floor for any sign
    rem = num - floor_q * den       # 0 <= rem < den

    if policy is RoundingPolicy.FLOOR:
        return floor_q
    if policy is RoundingPolicy.CEILING:
        return floor_q + (1 if rem else 0)
    if policy is RoundingPolicy.TOWARD_ZERO:
        return floor_q + (1 if rem and num < 0 else 0)

    twice = 2 * rem
    if twice < den:
        return floor_q
    if twice > den:
        return floor_q + 1
    # exact tie at one half
    if policy is RoundingPolicy.HALF_EVEN:
        return floor_q + (floor_q % 2)
    if policy is RoundingPolicy.HALF_AWAY:
        return floor_q + (1 if num >= 0 else 0)
    raise ValueError(f"unknown rounding policy: {policy}")
