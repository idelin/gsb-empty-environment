"""Truncated rational formal power series.

A series is represented modulo ``x ** order``: it stores exactly the
coefficients of degrees ``0, 1, ..., order - 1``.  All coefficients are
exact rational numbers.
"""

from __future__ import annotations

from fractions import Fraction
from numbers import Rational
from typing import Iterable, Sequence


RationalInput = int | Fraction | Rational


class TruncationError(ValueError):
    """Raised when an operation asks for coefficients outside its domain."""


class ZeroConstantError(ValueError):
    """Raised when a series with zero constant term is inverted."""


class NonZeroConstantError(ValueError):
    """Raised when composition has an inner series with a nonzero constant."""


def _as_fraction(value: RationalInput) -> Fraction:
    """Convert an exact rational input to :class:`Fraction`."""
    if isinstance(value, bool):
        raise TypeError("boolean coefficients are not accepted")
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value, 1)
    if isinstance(value, Rational):
        return Fraction(value.numerator, value.denominator)
    raise TypeError(f"exact rational coefficient required, got {type(value).__name__}")


def _check_order(order: object) -> int:
    if isinstance(order, bool) or not isinstance(order, int):
        raise TypeError("order must be an integer")
    if order < 0:
        raise ValueError("order must be non-negative")
    return order


def _check_common_order(order: object, inputs: Sequence["TruncatedPowerSeries"]) -> int:
    order = _check_order(order)
    for series in inputs:
        if not isinstance(series, TruncatedPowerSeries):
            raise TypeError("operation requires TruncatedPowerSeries instances")
        if order > series.order:
            raise TruncationError(
                f"target order {order} exceeds input order {series.order}"
            )
    return order


class TruncatedPowerSeries:
    """A finite representation of a rational formal power series modulo ``x^order``."""

    def __init__(
        self,
        coefficients: Iterable[RationalInput],
        order: int | None = None,
    ) -> None:
        values = [_as_fraction(value) for value in coefficients]
        if order is None:
            order = len(values)
        order = _check_order(order)

        if len(values) < order:
            values.extend([Fraction(0)] * (order - len(values)))
        elif len(values) > order:
            del values[order:]

        self.__coefficients: tuple[Fraction, ...] = tuple(values)
        self.__order = order

    @property
    def order(self) -> int:
        """Return the truncation order ``N`` for the residue modulo ``x^N``."""
        return self.__order

    @property
    def coefficients(self) -> tuple[Fraction, ...]:
        """Return coefficients from degree zero through ``order - 1``."""
        return self.__coefficients

    @classmethod
    def zero(cls, order: int) -> "TruncatedPowerSeries":
        """Create the zero series modulo ``x^order``."""
        return cls((), order)

    @classmethod
    def one(cls, order: int) -> "TruncatedPowerSeries":
        """Create the constant one series modulo ``x^order``."""
        _check_order(order)
        if order == 0:
            return cls.zero(0)
        return cls([Fraction(1)], order)

    def is_zero(self) -> bool:
        return not any(self.__coefficients)

    def __len__(self) -> int:
        return self.__order

    def __getitem__(self, degree: int) -> Fraction:
        if isinstance(degree, bool) or not isinstance(degree, int):
            raise TypeError("degree must be an integer")
        if not 0 <= degree < self.__order:
            raise IndexError(f"degree {degree} is outside modulo x^{self.__order}")
        return self.__coefficients[degree]

    def __iter__(self):
        return iter(self.__coefficients)

    def __neg__(self) -> "TruncatedPowerSeries":
        return TruncatedPowerSeries((-value for value in self.__coefficients), self.__order)

    def __add__(self, other: object) -> "TruncatedPowerSeries":
        if not isinstance(other, TruncatedPowerSeries):
            return NotImplemented
        return add(self, other)

    def __sub__(self, other: object) -> "TruncatedPowerSeries":
        if not isinstance(other, TruncatedPowerSeries):
            return NotImplemented
        return add(self, -other)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, TruncatedPowerSeries):
            return NotImplemented
        return (
            self.__order == other.__order
            and self.__coefficients == other.__coefficients
        )

    def __hash__(self) -> int:
        return hash((self.__order, self.__coefficients))

    def __repr__(self) -> str:
        terms = ", ".join(repr(value) for value in self.__coefficients)
        return f"TruncatedPowerSeries([{terms}], {self.__order})"


def add(
    left: TruncatedPowerSeries,
    right: TruncatedPowerSeries,
) -> TruncatedPowerSeries:
    """Add two truncated series.

    Domain: if ``left`` is known modulo ``x^A`` and ``right`` modulo
    ``x^B``, their sum is determined modulo ``x^min(A, B)``.  The output
    therefore has order ``min(A, B)``.
    """
    if not isinstance(left, TruncatedPowerSeries) or not isinstance(
        right, TruncatedPowerSeries
    ):
        raise TypeError("addition requires two TruncatedPowerSeries instances")

    order = min(left.order, right.order)
    return TruncatedPowerSeries(
        (left[degree] + right[degree] for degree in range(order)),
        order,
    )


def multiply(
    left: TruncatedPowerSeries,
    right: TruncatedPowerSeries,
    order: int,
) -> TruncatedPowerSeries:
    """Multiply and retain exactly the terms below ``order``.

    Domain: ``0 <= order <= min(left.order, right.order)``.  Coefficients
    below ``order`` depend only on input coefficients below ``order``; higher
    convolution terms are discarded rather than carried along silently.
    """
    _check_common_order(order, (left, right))

    product = [Fraction(0) for _ in range(order)]
    for i, left_coefficient in enumerate(left.coefficients[:order]):
        if left_coefficient == 0:
            continue
        for j in range(order - i):
            product[i + j] += left_coefficient * right[j]

    return TruncatedPowerSeries(product, order)


def inverse(
    series: TruncatedPowerSeries,
    order: int | None = None,
) -> TruncatedPowerSeries:
    """Return the multiplicative inverse modulo ``x^order``.

    Domain: ``series`` must have a nonzero constant coefficient, and
    ``1 <= order <= series.order``.  A nonzero constant gives a unique formal
    power-series inverse, and coefficient ``n`` is recursively determined by
    lower coefficients.

    A zero constant is an illegal inverse and raises ``ZeroConstantError``;
    it never produces a zero series as a misleading placeholder.
    """
    if not isinstance(series, TruncatedPowerSeries):
        raise TypeError("inverse requires a TruncatedPowerSeries instance")
    if order is None:
        order = series.order
    _check_common_order(order, (series,))

    if order == 0:
        raise TruncationError("multiplicative inverse requires a positive order")
    if series[0] == 0:
        raise ZeroConstantError(
            "a series with zero constant term has no multiplicative inverse"
        )

    constant = series[0]
    inverse_coefficients = [Fraction(0) for _ in range(order)]
    inverse_coefficients[0] = 1 / constant

    for degree in range(1, order):
        contribution = sum(
            series[k] * inverse_coefficients[degree - k]
            for k in range(1, degree + 1)
        )
        inverse_coefficients[degree] = -contribution / constant

    return TruncatedPowerSeries(inverse_coefficients, order)


def compose(
    outer: TruncatedPowerSeries,
    inner: TruncatedPowerSeries,
    order: int | None = None,
) -> TruncatedPowerSeries:
    """Return ``outer(inner(x))`` modulo ``x^order``.

    Domain: ``inner.order >= 1`` and ``inner[0] == 0``, with
    ``1 <= order <= min(outer.order, inner.order)``.  When the inner constant
    is zero, output coefficient ``n`` uses only outer coefficients through
    degree ``n`` because every positive power of the inner series has valuation
    at least one.

    If the inner constant is nonzero, omitted higher coefficients of the
    truncated outer series can affect every output coefficient, so the
    composition is not certified from these inputs and raises
    ``NonZeroConstantError``.
    """
    if not isinstance(outer, TruncatedPowerSeries) or not isinstance(
        inner, TruncatedPowerSeries
    ):
        raise TypeError("composition requires two TruncatedPowerSeries instances")
    if order is None:
        order = min(outer.order, inner.order)
    _check_common_order(order, (outer, inner))

    if order == 0:
        return TruncatedPowerSeries.zero(0)
    if inner[0] != 0:
        raise NonZeroConstantError(
            "composition of truncated series requires inner constant term to be zero"
        )

    result = [Fraction(0) for _ in range(order)]

    for degree in range(min(order, outer.order) - 1, -1, -1):
        shifted = [Fraction(0) for _ in range(order)]
        for i in range(order):
            if result[i] == 0:
                continue
            for j in range(order - i):
                shifted[i + j] += result[i] * inner[j]
        shifted[0] += outer[degree]
        result = shifted

    return TruncatedPowerSeries(result, order)
