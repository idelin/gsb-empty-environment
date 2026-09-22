from __future__ import annotations

from decimal import Decimal, ROUND_HALF_EVEN
from fractions import Fraction
from typing import Union


ExactTime = Union[int, str, Fraction, Decimal]
OUTPUT_DECIMALS = 6
ZERO = Fraction(0, 1)


def to_fraction(value: ExactTime) -> Fraction:
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid exact numeric value")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, Fraction):
        return value
    if isinstance(value, Decimal):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(Decimal(value))
    raise TypeError(
        f"exact int, str, Decimal, or Fraction is required; got {type(value).__name__}"
    )


def grid_time(origin: ExactTime, period: ExactTime, index: int) -> Fraction:
    if not isinstance(index, int) or isinstance(index, bool):
        raise TypeError("grid index must be an integer")
    start = to_fraction(origin)
    step = to_fraction(period)
    if step <= 0:
        raise ValueError("grid period must be positive")
    return start + index * step


def interpolate_linear(
    point_time: Fraction,
    left_time: Fraction,
    left_value: Fraction,
    right_time: Fraction,
    right_value: Fraction,
) -> Fraction:
    span = right_time - left_time
    if span <= 0:
        raise ValueError("linear interpolation requires an increasing time pair")
    weight = (point_time - left_time) / span
    return left_value + (right_value - left_value) * weight


def round_output(value: Fraction) -> Decimal:
    scale = 10**OUTPUT_DECIMALS
    numerator = value.numerator * scale
    denominator = value.denominator
    quotient, remainder = divmod(abs(numerator), denominator)
    doubled = remainder * 2
    round_up = doubled > denominator or (
        doubled == denominator and quotient % 2 == 1
    )
    if round_up:
        quotient += 1
    scaled = -quotient if numerator < 0 else quotient
    digits = str(abs(scaled)).zfill(OUTPUT_DECIMALS + 1)
    text = f"{digits[:-OUTPUT_DECIMALS]}.{digits[-OUTPUT_DECIMALS:]}"
    if scaled < 0:
        text = f"-{text}"
    return Decimal(text).quantize(Decimal("1e-6"), rounding=ROUND_HALF_EVEN)
